package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Dialect abstracts database-specific Flyway semantics.
type Dialect interface {
	Name() string
	Quote(ident string) string
	CreateSchemaHistoryTableStatements(table string) []string
	CurrentUserExpr() string
	CurrentSchemaExpr() string
	CleanSchema(ctx context.Context, exec dbExecutor, schema string) error
	DropSchemaDDL(schema string) string
	UnQuote(ident string) string
	HistoryTableExists(ctx context.Context, exec dbExecutor, table string) (bool, error)
	SchemaExists(ctx context.Context, exec dbExecutor, schema string) (bool, error)
	SchemaEmpty(ctx context.Context, exec dbExecutor, schema string) (bool, error)
	CreateSchemaDDL(schema string) string
	SupportsDDLTransactions() bool
}

func GetDialect(name string) (Dialect, error) {
	switch strings.ToLower(name) {
	case "postgres", "postgresql", "pgx":
		return postgresDialect{}, nil
	case "mysql", "mariadb":
		return mysqlDialect{}, nil
	default:
		return nil, fmt.Errorf("不支持的数据库方言: %s（当前支持 postgresql / mysql）", name)
	}
}

func splitQualifiedName(name string) []string {
	return strings.Split(name, ".")
}

func tableBaseName(name string) string {
	parts := splitQualifiedName(name)
	return parts[len(parts)-1]
}

func quoteQualified(d Dialect, name string) string {
	parts := splitQualifiedName(name)
	for i, p := range parts {
		parts[i] = d.Quote(p)
	}
	return strings.Join(parts, ".")
}

func queryStringList(ctx context.Context, exec dbExecutor, query string, args ...any) ([]string, error) {
	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func executeCleanStatements(ctx context.Context, exec dbExecutor, statements []string) error {
	for _, stmt := range statements {
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w\n语句: %s", err, stmt)
		}
	}
	return nil
}

// ---------------------------------------------------------------- PostgreSQL

type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgresql" }
func (postgresDialect) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (postgresDialect) CurrentUserExpr() string       { return "current_user" }
func (postgresDialect) CurrentSchemaExpr() string     { return "current_schema()" }
func (postgresDialect) SupportsDDLTransactions() bool { return true }

func (postgresDialect) UnQuote(ident string) string {
	const open, close, escaped = `"`, `"`, `""`
	if strings.HasPrefix(ident, open) && strings.HasSuffix(ident, close) {
		ident = ident[len(open) : len(ident)-len(close)]
		ident = strings.ReplaceAll(ident, escaped, close)
	}
	return ident
}

func (d postgresDialect) CreateSchemaHistoryTableStatements(table string) []string {
	base := tableBaseName(table)
	qualified := quoteQualified(d, table)
	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
    "installed_rank" INT NOT NULL,
    "version" VARCHAR(50),
    "description" VARCHAR(200) NOT NULL,
    "type" VARCHAR(20) NOT NULL,
    "script" VARCHAR(1000) NOT NULL,
    "checksum" INTEGER,
    "installed_by" VARCHAR(100) NOT NULL,
    "installed_on" TIMESTAMP NOT NULL DEFAULT now(),
    "execution_time" INTEGER NOT NULL,
    "success" BOOLEAN NOT NULL
)`, qualified),
		fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT %s PRIMARY KEY ("installed_rank")`, qualified, d.Quote(base+"_pk")),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("success")`, d.Quote(base+"_s_idx"), qualified),
	}
}

func (d postgresDialect) CreateSchemaDDL(schema string) string {
	return fmt.Sprintf("CREATE SCHEMA %s", d.Quote(schema))
}

func (d postgresDialect) DropSchemaDDL(schema string) string {
	return fmt.Sprintf("DROP SCHEMA %s CASCADE", d.Quote(schema))
}

func (d postgresDialect) HistoryTableExists(ctx context.Context, exec dbExecutor, table string) (bool, error) {
	parts := splitQualifiedName(table)
	schema := ""
	name := parts[len(parts)-1]
	if len(parts) > 1 {
		schema = parts[len(parts)-2]
	}
	var exists bool
	err := exec.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = COALESCE(NULLIF($1, ''), current_schema())
  AND c.relname = $2
  AND c.relkind = 'r'
)`, schema, name).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (d postgresDialect) SchemaExists(ctx context.Context, exec dbExecutor, schema string) (bool, error) {
	var n int
	err := exec.QueryRowContext(ctx, `SELECT COUNT(1) FROM pg_catalog.pg_namespace WHERE nspname = $1`, schema).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d postgresDialect) SchemaEmpty(ctx context.Context, exec dbExecutor, schema string) (bool, error) {
	q := `SELECT NOT EXISTS (
    SELECT c.oid FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind IN ('r', 'v', 'S', 't')
  UNION ALL
    SELECT t.oid FROM pg_catalog.pg_type t
    JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = t.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL AND t.typcategory NOT IN ('A', 'C')
  UNION ALL
    SELECT p.oid FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL
)`
	var empty bool
	if err := exec.QueryRowContext(ctx, q, schema).Scan(&empty); err != nil {
		return false, err
	}
	return empty, nil
}

func postgresVersionAtLeast(ctx context.Context, exec dbExecutor, major int) (bool, error) {
	var version int
	if err := exec.QueryRowContext(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		return false, err
	}
	return version >= major*10000, nil
}

func (d postgresDialect) CleanSchema(ctx context.Context, exec dbExecutor, schema string) error {
	qualified := func(name string) string {
		return d.Quote(schema) + "." + d.Quote(name)
	}

	pg93, err := postgresVersionAtLeast(ctx, exec, 903)
	if err != nil {
		return fmt.Errorf("读取 PostgreSQL 版本失败: %w", err)
	}
	if pg93 {
		names, qerr := queryStringList(ctx, exec, `SELECT relname FROM pg_catalog.pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'm' AND n.nspname = $1`, schema)
		if qerr != nil {
			return fmt.Errorf("查询 PostgreSQL materialized view 失败: %w", qerr)
		}
		stmts := make([]string, 0, len(names))
		for _, name := range names {
			stmts = append(stmts, "DROP MATERIALIZED VIEW IF EXISTS "+qualified(name)+" CASCADE")
		}
		if err := executeCleanStatements(ctx, exec, stmts); err != nil {
			return err
		}
	}

	names, err := queryStringList(ctx, exec, `SELECT relname FROM pg_catalog.pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace LEFT JOIN pg_depend dep ON dep.objid = c.oid AND dep.deptype = 'e' WHERE c.relkind = 'v' AND n.nspname = $1 AND dep.objid IS NULL`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL view 失败: %w", err)
	}
	stmts := make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP VIEW IF EXISTS "+qualified(name)+" CASCADE")
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	names, err = queryStringList(ctx, exec, `SELECT t.table_name FROM information_schema.tables t LEFT JOIN pg_depend dep ON dep.objid = (quote_ident(t.table_schema)||'.'||quote_ident(t.table_name))::regclass::oid AND dep.deptype = 'e' WHERE t.table_schema = $1 AND t.table_type = 'BASE TABLE' AND dep.objid IS NULL AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits inh WHERE inh.inhrelid = (quote_ident(t.table_schema)||'.'||quote_ident(t.table_name))::regclass::oid)`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL table 失败: %w", err)
	}
	stmts = make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP TABLE "+qualified(name)+" CASCADE")
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	baseTypesQuery := `SELECT typname, typcategory FROM pg_catalog.pg_type t LEFT JOIN pg_depend dep ON dep.objid = t.oid AND dep.deptype = 'e' WHERE (t.typrelid = 0 OR (SELECT c.relkind = 'c' FROM pg_catalog.pg_class c WHERE c.oid = t.typrelid)) AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_type el WHERE el.oid = t.typelem AND el.typarray = t.oid) AND t.typnamespace IN (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = $1) AND dep.objid IS NULL AND t.typtype != 'd'`
	baseTypeStatements := func(recreate bool) error {
		rows, qerr := exec.QueryContext(ctx, baseTypesQuery, schema)
		if qerr != nil {
			return fmt.Errorf("查询 PostgreSQL base type 失败: %w", qerr)
		}
		type baseType struct {
			name     string
			category string
		}
		var types []baseType
		for rows.Next() {
			var item baseType
			if serr := rows.Scan(&item.name, &item.category); serr != nil {
				rows.Close()
				return serr
			}
			types = append(types, item)
		}
		rerr := rows.Err()
		rows.Close()
		if rerr != nil {
			return rerr
		}
		statements := make([]string, 0, len(types))
		for _, item := range types {
			statements = append(statements, "DROP TYPE IF EXISTS "+qualified(item.name)+" CASCADE")
		}
		if recreate {
			for _, item := range types {
				if item.category == "P" || item.category == "U" {
					statements = append(statements, "CREATE TYPE "+qualified(item.name))
				}
			}
		}
		return executeCleanStatements(ctx, exec, statements)
	}
	if err := baseTypeStatements(true); err != nil {
		return err
	}

	pg11, err := postgresVersionAtLeast(ctx, exec, 11)
	if err != nil {
		return fmt.Errorf("读取 PostgreSQL 版本失败: %w", err)
	}
	isAggregate := "pg_proc.proisagg"
	isProcedure := "FALSE"
	if pg11 {
		isAggregate = "pg_proc.prokind = 'a'"
		isProcedure = "pg_proc.prokind = 'p'"
	}
	routineQuery := fmt.Sprintf(`SELECT proname, oidvectortypes(proargtypes) AS args, %s AS agg, %s AS proc FROM pg_proc INNER JOIN pg_namespace ns ON pg_proc.pronamespace = ns.oid LEFT JOIN pg_depend dep ON dep.objid = pg_proc.oid AND dep.deptype = 'e' WHERE ns.nspname = $1 AND dep.objid IS NULL`, isAggregate, isProcedure)
	rows, err := exec.QueryContext(ctx, routineQuery, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL routine 失败: %w", err)
	}
	stmts = nil
	for rows.Next() {
		var name, args string
		var aggregate, procedure bool
		if err := rows.Scan(&name, &args, &aggregate, &procedure); err != nil {
			rows.Close()
			return err
		}
		kind := "FUNCTION"
		if aggregate {
			kind = "AGGREGATE"
		} else if procedure {
			kind = "PROCEDURE"
		}
		stmts = append(stmts, "DROP "+kind+" IF EXISTS "+qualified(name)+"("+args+") CASCADE")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	names, err = queryStringList(ctx, exec, `SELECT t.typname FROM pg_catalog.pg_type t INNER JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = $1 AND t.typtype = 'e'`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL enum 失败: %w", err)
	}
	stmts = make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP TYPE "+qualified(name))
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	names, err = queryStringList(ctx, exec, `SELECT t.typname FROM pg_catalog.pg_type t LEFT JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace LEFT JOIN pg_depend dep ON dep.objid = t.oid AND dep.deptype = 'e' WHERE t.typtype = 'd' AND n.nspname = $1 AND dep.objid IS NULL`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL domain 失败: %w", err)
	}
	stmts = make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP DOMAIN IF EXISTS "+qualified(name)+" CASCADE")
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	names, err = queryStringList(ctx, exec, `SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = $1`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL sequence 失败: %w", err)
	}
	stmts = make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP SEQUENCE IF EXISTS "+qualified(name))
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	if err := baseTypeStatements(false); err != nil {
		return err
	}

	names, err = queryStringList(ctx, exec, `SELECT c.collname FROM pg_catalog.pg_collation c JOIN pg_namespace n ON c.collnamespace = n.oid LEFT JOIN pg_depend dep ON dep.objid = c.oid AND dep.deptype = 'e' WHERE n.nspname = $1 AND dep.objid IS NULL`, schema)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL collation 失败: %w", err)
	}
	stmts = make([]string, 0, len(names))
	for _, name := range names {
		stmts = append(stmts, "DROP COLLATION IF EXISTS "+qualified(name)+" CASCADE")
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	var extensionsTableExists bool
	if err := exec.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'pg_extension')`).Scan(&extensionsTableExists); err != nil {
		return fmt.Errorf("检查 PostgreSQL pg_extension 失败: %w", err)
	}
	if extensionsTableExists {
		names, err = queryStringList(ctx, exec, `SELECT e.extname FROM pg_extension e LEFT JOIN pg_namespace n ON n.oid = e.extnamespace LEFT JOIN pg_roles r ON r.oid = e.extowner WHERE n.nspname = $1 AND r.rolname = current_user`, schema)
		if err != nil {
			return fmt.Errorf("查询 PostgreSQL extension 失败: %w", err)
		}
		stmts = make([]string, 0, len(names))
		for _, name := range names {
			stmts = append(stmts, "DROP EXTENSION IF EXISTS "+d.Quote(name)+" CASCADE")
		}
		if err := executeCleanStatements(ctx, exec, stmts); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- MySQL

type mysqlDialect struct{}

func (mysqlDialect) Name() string { return "mysql" }
func (mysqlDialect) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}
func (mysqlDialect) SupportsDDLTransactions() bool { return false }
func (mysqlDialect) CurrentUserExpr() string       { return "SUBSTRING_INDEX(CURRENT_USER(), '@', 1)" }
func (mysqlDialect) CurrentSchemaExpr() string     { return "DATABASE()" }

func (mysqlDialect) UnQuote(ident string) string {
	const quote = "`"
	if strings.HasPrefix(ident, quote) && strings.HasSuffix(ident, quote) {
		return ident[len(quote) : len(ident)-len(quote)]
	}
	return ident
}

func (d mysqlDialect) CreateSchemaHistoryTableStatements(table string) []string {
	base := tableBaseName(table)
	qualified := quoteQualified(d, table)
	return []string{
		fmt.Sprintf("CREATE TABLE %s (\n"+
			"    `installed_rank` INT NOT NULL,\n"+
			"    `version` VARCHAR(50),\n"+
			"    `description` VARCHAR(200) NOT NULL,\n"+
			"    `type` VARCHAR(20) NOT NULL,\n"+
			"    `script` VARCHAR(1000) NOT NULL,\n"+
			"    `checksum` INT,\n"+
			"    `installed_by` VARCHAR(100) NOT NULL,\n"+
			"    `installed_on` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,\n"+
			"    `execution_time` INT NOT NULL,\n"+
			"    `success` BOOL NOT NULL,\n"+
			"    CONSTRAINT %s PRIMARY KEY (`installed_rank`)\n"+
			") ENGINE=InnoDB",
			qualified, d.Quote(base+"_pk")),
		fmt.Sprintf("CREATE INDEX %s ON %s (`success`)", d.Quote(base+"_s_idx"), qualified),
	}
}

func (d mysqlDialect) CreateSchemaDDL(schema string) string {
	return fmt.Sprintf("CREATE SCHEMA %s", d.Quote(schema))
}

func (d mysqlDialect) DropSchemaDDL(schema string) string {
	return fmt.Sprintf("DROP SCHEMA %s", d.Quote(schema))
}

func (d mysqlDialect) HistoryTableExists(ctx context.Context, exec dbExecutor, table string) (bool, error) {
	parts := splitQualifiedName(table)
	schema := ""
	name := parts[len(parts)-1]
	if len(parts) > 1 {
		schema = parts[len(parts)-2]
	}
	var n int
	err := exec.QueryRowContext(ctx, `SELECT COUNT(1)
FROM information_schema.tables
WHERE table_schema = COALESCE(NULLIF(?, ''), DATABASE())
  AND table_name = ?
  AND table_type = 'BASE TABLE'`, schema, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d mysqlDialect) SchemaExists(ctx context.Context, exec dbExecutor, schema string) (bool, error) {
	var n int
	err := exec.QueryRowContext(ctx, `SELECT COUNT(1) FROM information_schema.schemata WHERE schema_name = ?`, schema).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d mysqlDialect) SchemaEmpty(ctx context.Context, exec dbExecutor, schema string) (bool, error) {
	const base = `SELECT COALESCE(SUM(found), 0) FROM (
 (SELECT 1 AS found FROM information_schema.tables WHERE table_schema = ? LIMIT 1) UNION ALL
 (SELECT 1 AS found FROM information_schema.views WHERE table_schema = ? LIMIT 1) UNION ALL
 (SELECT 1 AS found FROM information_schema.table_constraints WHERE table_schema = ? LIMIT 1) UNION ALL
 (SELECT 1 AS found FROM information_schema.triggers WHERE event_object_schema = ? LIMIT 1) UNION ALL
 (SELECT 1 AS found FROM information_schema.routines WHERE routine_schema = ? LIMIT 1)%s
) AS all_found`
	args := []any{schema, schema, schema, schema, schema}
	if d.eventSchedulerQueryable(ctx, exec) {
		args = append(args, schema)
		return querySchemaEmpty(ctx, exec, base, args)
	}
	return querySchemaEmpty(ctx, exec, base, args)
}

func querySchemaEmpty(ctx context.Context, exec dbExecutor, base string, args []any) (bool, error) {
	suffix := ""
	if len(args) == 6 {
		suffix = " UNION ALL (SELECT 1 AS found FROM information_schema.events WHERE event_schema = ? LIMIT 1)"
	}
	var n int
	if err := exec.QueryRowContext(ctx, fmt.Sprintf(base, suffix), args...).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// eventSchedulerQueryable mirrors MySQLDatabase.eventSchedulerQueryable:
// MySQL is always queryable; MariaDB is probed once and any failure means the
// event phase must be skipped.
func (d mysqlDialect) eventSchedulerQueryable(ctx context.Context, exec dbExecutor) bool {
	var version string
	if err := exec.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return false
	}
	if !strings.Contains(strings.ToLower(version), "mariadb") {
		return true
	}
	var eventName sql.NullString
	err := exec.QueryRowContext(ctx, `SELECT event_name FROM information_schema.events LIMIT 1`).Scan(&eventName)
	return err == nil || err == sql.ErrNoRows
}

func (d mysqlDialect) CleanSchema(ctx context.Context, exec dbExecutor, schema string) error {
	qualified := func(name string) string {
		return d.Quote(schema) + "." + d.Quote(name)
	}

	// MySQL always exposes information_schema.events; MariaDB can reject it
	// while the scheduler is OFF or DISABLED. The probe is deliberately
	// independent of an empty result set: sql.ErrNoRows means queryable.
	if d.eventSchedulerQueryable(ctx, exec) {
		eventNames, qerr := queryStringList(ctx, exec, `SELECT event_name FROM information_schema.events WHERE event_schema = ?`, schema)
		if qerr != nil {
			return fmt.Errorf("查询 MySQL event 失败: %w", qerr)
		}
		stmts := make([]string, 0, len(eventNames))
		for _, name := range eventNames {
			stmts = append(stmts, "DROP EVENT "+qualified(name))
		}
		if err := executeCleanStatements(ctx, exec, stmts); err != nil {
			return err
		}
	}

	routineRows, err := exec.QueryContext(ctx, `SELECT routine_name, routine_type FROM information_schema.routines WHERE routine_schema = ?`, schema)
	if err != nil {
		return fmt.Errorf("查询 MySQL routine 失败: %w", err)
	}
	var routines []struct {
		name string
		typ  string
	}
	for routineRows.Next() {
		var item struct {
			name string
			typ  string
		}
		if err := routineRows.Scan(&item.name, &item.typ); err != nil {
			routineRows.Close()
			return err
		}
		routines = append(routines, item)
	}
	rerr := routineRows.Err()
	routineRows.Close()
	if rerr != nil {
		return rerr
	}
	stmts := make([]string, 0, len(routines))
	for _, item := range routines {
		stmts = append(stmts, "DROP "+strings.ToUpper(item.typ)+" "+qualified(item.name))
	}
	if err := executeCleanStatements(ctx, exec, stmts); err != nil {
		return err
	}

	viewNames, err := queryStringList(ctx, exec, `SELECT table_name FROM information_schema.views WHERE table_schema = ?`, schema)
	if err != nil {
		return fmt.Errorf("查询 MySQL view 失败: %w", err)
	}
	for _, name := range viewNames {
		stmt := "DROP VIEW " + qualified(name)
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w\n语句: %s", err, stmt)
		}
	}

	if _, err := exec.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	tableNames, err := queryStringList(ctx, exec, `SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type IN ('BASE TABLE', 'SYSTEM VERSIONED')`, schema)
	if err != nil {
		return fmt.Errorf("查询 MySQL table 失败: %w", err)
	}
	for _, name := range tableNames {
		stmt := "DROP TABLE " + qualified(name)
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w\n语句: %s", err, stmt)
		}
	}
	if _, err := exec.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		return err
	}

	sequenceNames, err := queryStringList(ctx, exec, `SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'SEQUENCE'`, schema)
	if err != nil {
		return fmt.Errorf("查询 MySQL sequence 失败: %w", err)
	}
	for _, name := range sequenceNames {
		stmt := "DROP SEQUENCE " + qualified(name)
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w\n语句: %s", err, stmt)
		}
	}
	return nil
}
