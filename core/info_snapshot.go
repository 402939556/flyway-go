package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// InfoSnapshot is the single-connection view DbInfo.info() builds before the
// CLI applies a migration filter. Schema version uses every migration; rendered
// rows and undoable status use the filtered subset.
type InfoSnapshot struct {
	Infos []*MigrationInfo

	HistoryExists          bool
	HistoryTableName       string
	AllSchemasEmpty        bool
	DatabaseName           string
	DatabaseProductName    string
	DatabaseProductVersion string

	EnvironmentName string
	SchemaName      string
}

// InfoSnapshot mirrors DbInfo.info(): one connection, one history-table probe,
// ignore-all patterns, and auto-baseline only when the schemas are not empty.
func (f *Flyway) InfoSnapshot(ctx context.Context) (InfoSnapshot, error) {
	if err := f.runCallbacks(ctx, EventBeforeInfo); err != nil {
		return InfoSnapshot{}, err
	}
	snapshot, err := f.collectInfoSnapshot(ctx)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterInfoError)
		return InfoSnapshot{}, err
	}
	if err := f.runCallbacks(ctx, EventAfterInfo); err != nil {
		return InfoSnapshot{}, err
	}
	return snapshot, nil
}

func (f *Flyway) collectInfoSnapshot(ctx context.Context) (InfoSnapshot, error) {
	conn, err := f.commandConn(ctx, "info 状态读取")
	if err != nil {
		return InfoSnapshot{}, err
	}
	defer conn.Close()

	exists, err := f.history.existsWithExecutor(ctx, conn)
	if err != nil {
		return InfoSnapshot{}, err
	}
	applied, err := f.history.AllWithExecutorKnownExists(ctx, conn, exists)
	if err != nil {
		return InfoSnapshot{}, err
	}
	schemas, err := f.targetSchemas(ctx)
	if err != nil {
		return InfoSnapshot{}, err
	}
	allEmpty, err := f.allSchemasEmptyWithExecutor(ctx, conn, schemas)
	if err != nil {
		return InfoSnapshot{}, err
	}
	dbName, productName, productVersion, err := f.databaseProductInfo(ctx, conn)
	if err != nil {
		return InfoSnapshot{}, err
	}

	var baseline *Version
	if !allEmpty && f.cfg.BaselineOnMigrate && !f.cfg.SkipExecutingMigrations && !exists {
		parsed, perr := ParseVersion(f.cfg.BaselineVersion)
		if perr != nil {
			return InfoSnapshot{}, fmt.Errorf("非法 baselineVersion %q: %w", f.cfg.BaselineVersion, perr)
		}
		baseline = &parsed
	}
	target, err := f.targetVersion()
	if err != nil {
		return InfoSnapshot{}, err
	}
	infos, _, err := BuildMigrationInfos(f.resolved, applied, target, f.cfg.OutOfOrder, IgnoreAllPatterns(), f.cfg.FailOnMissingTarget, baseline)
	if err != nil {
		return InfoSnapshot{}, err
	}

	environment := f.cfg.EnvironmentName
	if environment == "" {
		environment = "default"
	}
	return InfoSnapshot{
		Infos:                  infos,
		HistoryExists:          exists,
		HistoryTableName:       f.history.QuotedName(),
		AllSchemasEmpty:        allEmpty,
		DatabaseName:           dbName,
		DatabaseProductName:    productName,
		DatabaseProductVersion: productVersion,
		EnvironmentName:        environment,
		SchemaName:             strings.Join(f.cfg.Schemas, ", "),
	}, nil
}

// Result applies MigrationFilterImpl, then computes undoable status from that
// filtered collection. Schema version stays on the unfiltered collection.
func (s InfoSnapshot) Result(filter *InfoFilter) InfoResult {
	filtered := make([]*MigrationInfo, 0, len(s.Infos))
	for _, info := range s.Infos {
		if shouldIncludeInfo(info, filter) {
			filtered = append(filtered, info)
		}
	}
	undos := undoCandidates(filtered)
	entries := make([]InfoEntry, 0, len(filtered))
	for _, info := range filtered {
		entries = append(entries, infoEntryFor(info, undos))
	}
	return InfoResult{
		SchemaVersion:          infoSchemaVersion(s.Infos),
		Entries:                entries,
		AllSchemasEmpty:        s.AllSchemasEmpty,
		DatabaseName:           s.DatabaseName,
		DatabaseProductName:    s.DatabaseProductName,
		DatabaseProductVersion: s.DatabaseProductVersion,
		EnvironmentName:        s.EnvironmentName,
		SchemaName:             s.SchemaName,
		HistoryExists:          s.HistoryExists,
		HistoryTableName:       s.HistoryTableName,
	}
}

func (f *Flyway) allSchemasEmptyWithExecutor(ctx context.Context, exec dbExecutor, schemas []string) (bool, error) {
	for _, schema := range schemas {
		exists, err := f.dialect.SchemaExists(ctx, exec, schema)
		if err != nil {
			return false, fmt.Errorf("检查 schema %s 是否存在失败: %w", schema, err)
		}
		if !exists {
			continue
		}
		empty, err := f.dialect.SchemaEmpty(ctx, exec, schema)
		if err != nil {
			return false, fmt.Errorf("检查 schema %s 是否为空失败: %w", schema, err)
		}
		if !empty {
			return false, nil
		}
	}
	return true, nil
}

func (f *Flyway) databaseProductInfo(ctx context.Context, exec dbExecutor) (string, string, string, error) {
	switch f.dialect.Name() {
	case "postgresql":
		var name, versionNum string
		if err := exec.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
			return "", "", "", fmt.Errorf("读取当前数据库名失败: %w", err)
		}
		if err := exec.QueryRowContext(ctx, "SHOW server_version_num").Scan(&versionNum); err != nil {
			return "", "", "", fmt.Errorf("读取 PostgreSQL 版本失败: %w", err)
		}
		return name, "PostgreSQL", formatPostgresProductVersion(versionNum), nil
	case "mysql":
		var name, version string
		if err := exec.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err != nil {
			return "", "", "", fmt.Errorf("读取当前数据库名失败: %w", err)
		}
		if err := exec.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
			return "", "", "", fmt.Errorf("读取 MySQL 版本失败: %w", err)
		}
		product := "MySQL"
		if strings.Contains(strings.ToLower(version), "mariadb") {
			product = "MariaDB"
		}
		return name, product, formatTwoVersionSegments(version), nil
	default:
		return "", "", "", fmt.Errorf("不支持的数据库方言: %s", f.dialect.Name())
	}
}

// formatPostgresProductVersion turns server_version_num into the short product
// version shown by the CLI. 160016 and 161600 both render as 16.16.
func formatPostgresProductVersion(serverVersionNum string) string {
	n, err := strconv.Atoi(strings.TrimSpace(serverVersionNum))
	if err != nil || n < 0 {
		return strings.TrimSpace(serverVersionNum)
	}
	major := n / 10000
	minor := n % 100
	if minor == 0 {
		minor = (n % 10000) / 100
	}
	return strconv.Itoa(major) + "." + strconv.Itoa(minor)
}

func formatTwoVersionSegments(version string) string {
	segments := make([]string, 0, 2)
	current := strings.Builder{}
	flush := func() {
		if current.Len() == 0 || len(segments) >= 2 {
			current.Reset()
			return
		}
		segments = append(segments, current.String())
		current.Reset()
	}
	for _, r := range version {
		if r >= '0' && r <= '9' {
			if len(segments) < 2 {
				current.WriteRune(r)
			}
			continue
		}
		flush()
		if len(segments) >= 2 {
			break
		}
	}
	flush()
	if len(segments) == 0 {
		return strings.TrimSpace(version)
	}
	if len(segments) == 1 {
		return segments[0]
	}
	return segments[0] + "." + segments[1]
}
