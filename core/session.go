package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// configureSessionSchema makes a dedicated database session use the configured
// default Flyway schema. Migration execution, callbacks and lock acquisition all
// operate on pooled connections, so schema state must be explicit every time.
func (f *Flyway) configureSessionSchema(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return fmt.Errorf("配置 schema 会话失败: 连接为空")
	}
	return f.configureSessionSchemaWithExecutor(ctx, conn)
}

// connectRetrySleep is a variable so tests can verify retry/backoff behavior
// without waiting for real seconds.
var connectRetrySleep = func(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// openConn is the single entry point for obtaining a database session. It
// mirrors JdbcUtils.openConnection and JdbcConnectionFactory.openConnection:
// retry failed connections with exponential backoff, then run initSql on every
// newly opened connection without callbacks, placeholder replacement, batching,
// or an enclosing transaction.
func (f *Flyway) openConn(ctx context.Context) (*sql.Conn, error) {
	if f.db == nil {
		return nil, fmt.Errorf("必须提供数据库连接")
	}
	interval := 120
	if f.cfg.ConnectRetriesInterval != nil {
		interval = *f.cfg.ConnectRetriesInterval
	}
	delaySeconds := 1
	retries := 0
	for {
		conn, err := f.db.Conn(ctx)
		if err == nil {
			err = conn.PingContext(ctx)
		}
		if err == nil {
			// The database version is required by the SQL parser before initSql is
			// executed. JdbcConnectionFactory determines it from its first physical
			// connection, before Flyway installs the connection initializer.
			if f.dbVersion == 0 && f.dialect != nil && f.dialect.Name() == "postgresql" {
				var serverVersion sql.NullString
				if versionErr := conn.QueryRowContext(ctx, "SHOW server_version_num").Scan(&serverVersion); versionErr == nil {
					if n, convErr := strconv.Atoi(strings.TrimSpace(serverVersion.String)); convErr == nil {
						f.dbVersion = n
					}
				}
			}
			if initErr := f.executeInitSQL(ctx, conn); initErr != nil {
				_ = conn.Close()
				return nil, initErr
			}
			return conn, nil
		}
		if conn != nil {
			_ = conn.Close()
		}
		if retries >= f.cfg.ConnectRetries {
			return nil, fmt.Errorf("连接数据库失败: %w", err)
		}
		retries++
		f.warn("Connection error: %v\nRetrying in %d sec...", err, delaySeconds)
		if sleepErr := connectRetrySleep(ctx, time.Duration(delaySeconds)*time.Second); sleepErr != nil {
			return nil, fmt.Errorf("连接数据库失败: %w", sleepErr)
		}
		delaySeconds *= 2
		if delaySeconds > interval {
			delaySeconds = interval
		}
	}
}

func (f *Flyway) executeInitSQL(ctx context.Context, conn *sql.Conn) error {
	if conn == nil || f.cfg.InitSQL == "" {
		return nil
	}
	statements, err := ParseSQLScript(f.cfg.InitSQL, f.dialect.Name(), "initSql", f.dbVersion)
	if err != nil {
		return fmt.Errorf("初始化 SQL 解析失败: %w", err)
	}
	for _, stmt := range statements {
		if _, err := conn.ExecContext(ctx, stmt.SQL); err != nil {
			return fmt.Errorf("执行 initSql 失败: %w\n语句: %s", err, stmt.SQL)
		}
	}
	return nil
}

// commandConn reserves one pool connection for a complete command-level database
// operation. Flyway's JDBC execution templates use a single session, whereas
// database/sql may otherwise check the schema, read history, and write history
// on different pooled connections with different search_path/current database.
func (f *Flyway) commandConn(ctx context.Context, purpose string) (*sql.Conn, error) {
	conn, err := f.openConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取%s连接失败: %w", purpose, err)
	}
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", purpose, err)
	}
	return conn, nil
}

// configureSessionTimeZoneWithExecutor mirrors PgJDBC's session timezone from
// the JVM default zone. It is safe to call before the target schema exists and
// is required before the first installed_on write.
func (f *Flyway) configureSessionTimeZoneWithExecutor(ctx context.Context, exec dbExecutor) error {
	if f.dialect.Name() != "postgresql" {
		return nil
	}
	var applied sql.NullString
	if err := exec.QueryRowContext(ctx, "SELECT set_config('TimeZone', $1, false)", postgresSessionTimeZone()).Scan(&applied); err != nil {
		return fmt.Errorf("配置 PostgreSQL 会话时区失败: %w", err)
	}
	return nil
}

func (f *Flyway) configureSessionSchemaWithExecutor(ctx context.Context, exec dbExecutor) error {
	if len(f.schemas) == 0 || f.schemas[0] == "" {
		return fmt.Errorf("无法确定当前 schema")
	}
	schema := f.schemas[0]

	if err := f.configureSessionTimeZoneWithExecutor(ctx, exec); err != nil {
		return err
	}

	switch f.dialect.Name() {
	case "postgresql":
		exists, err := f.dialect.SchemaExists(ctx, exec, schema)
		if err != nil {
			return fmt.Errorf("检查当前 schema %s 是否存在失败: %w", schema, err)
		}
		if !exists {
			return nil
		}
		var current sql.NullString
		if err := exec.QueryRowContext(ctx, "SHOW search_path").Scan(&current); err != nil {
			return fmt.Errorf("读取 PostgreSQL search_path 失败: %w", err)
		}
		next, changed := postgresSearchPathForSchema(current.String, schema, f.dialect.Quote(schema))
		if !changed {
			return nil
		}
		var applied sql.NullString
		if err := exec.QueryRowContext(ctx, "SELECT set_config('search_path', $1, false)", next).Scan(&applied); err != nil {
			return fmt.Errorf("切换当前 schema 到 %s 失败: %w", schema, err)
		}
	case "mysql":
		exists, err := f.dialect.SchemaExists(ctx, exec, schema)
		if err != nil {
			return fmt.Errorf("检查当前 schema %s 是否存在失败: %w", schema, err)
		}
		if !exists {
			return nil
		}
		if _, err := exec.ExecContext(ctx, "USE "+f.dialect.Quote(schema)); err != nil {
			return fmt.Errorf("切换当前 schema 到 %s 失败: %w", schema, err)
		}
	default:
		return fmt.Errorf("不支持的数据库方言: %s", f.dialect.Name())
	}
	return nil
}

// postgresSessionTimeZone selects a PostgreSQL zone that matches the JVM
// default zone used by the official Java implementation. A valid TZ setting is
// preferred because it retains DST rules. When only an offset is available, use
// a PostgreSQL-recognised fixed-offset zone.
func postgresSessionTimeZone() string {
	if zone := strings.TrimSpace(os.Getenv("TZ")); zone != "" {
		zone = strings.TrimPrefix(zone, ":")
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc.String()
		}
	}
	if time.Local != nil {
		name := strings.TrimSpace(time.Local.String())
		if name != "" && name != "Local" {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc.String()
			}
		}
		_, offset := time.Now().In(time.Local).Zone()
		return postgresFixedOffsetZone(offset)
	}
	return "UTC"
}

func postgresFixedOffsetZone(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return "UTC"
	}
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	if minutes == 0 {
		// Etc/GMT signs are intentionally inverted: Etc/GMT-8 is UTC+08:00.
		etcSign := "-"
		if sign == "-" {
			etcSign = "+"
		}
		return fmt.Sprintf("Etc/GMT%s%d", etcSign, hours)
	}
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}

// postgresSearchPathForSchema mirrors PostgreSQLConnection.changeCurrentSchemaTo:
// an unchanged or already-leading schema is left alone; otherwise the quoted
// target schema is prepended to the original search_path.
func postgresSearchPathForSchema(current, schema, quotedSchema string) (string, bool) {
	if schema == current || strings.HasPrefix(current, schema+",") {
		return current, false
	}
	if strings.TrimSpace(current) != "" {
		return quotedSchema + "," + current, true
	}
	return quotedSchema, true
}
