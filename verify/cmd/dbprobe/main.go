package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

const usage = `dbprobe - Flyway parity database inspector

usage:
  dbprobe -driver postgres|mysql -dsn DSN -action reset|history|objects|query|exec [-schema SCHEMA] [-schemas CSV] [-table TABLE] [-sql SQL]

Canonical output:
  HISTORY<TAB>rank<TAB>version|NULL<TAB>description<TAB>type<TAB>script<TAB>checksum|NULL<TAB>installed_by<TAB>success
  OBJECT<TAB>kind<TAB>identity
  QUERY<TAB>sql
  DATA<TAB>value1<TAB>value2...
`

func main() {
	driver := flag.String("driver", "", "postgres or mysql")
	dsn := flag.String("dsn", "", "database DSN")
	action := flag.String("action", "", "reset, history, objects or query")
	schema := flag.String("schema", "", "schema/database name (empty means current)")
	schemasText := flag.String("schemas", "", "comma-separated schema/database names; overrides -schema")
	table := flag.String("table", "flyway_schema_history", "schema history table")
	sqlText := flag.String("sql", "", "SQL query for the query action")
	flag.Usage = func() { fmt.Fprint(flag.CommandLine.Output(), usage) }
	flag.Parse()

	if *driver == "" || *dsn == "" || *action == "" {
		flag.Usage()
		os.Exit(2)
	}
	drv := strings.ToLower(*driver)
	if drv != "postgres" && drv != "postgresql" && drv != "mysql" {
		fatalf("unsupported driver: %s", *driver)
	}

	db, err := sql.Open(drv, *dsn)
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		fatalf("ping database: %v", err)
	}

	resolvedSchemas, err := resolveSchemas(ctx, db, drv, *schemasText, *schema)
	if err != nil {
		fatalf("resolve schema: %v", err)
	}

	switch strings.ToLower(*action) {
	case "reset":
		if err := resetDatabase(ctx, db, drv, resolvedSchemas); err != nil {
			fatalf("reset database: %v", err)
		}
	case "history":
		rows, err := historyRows(ctx, db, drv, resolvedSchemas[0], *table)
		if err != nil {
			fatalf("read history: %v", err)
		}
		for _, row := range rows {
			fmt.Println(row)
		}
	case "objects":
		rows, err := objectRows(ctx, db, drv, resolvedSchemas, *table)
		if err != nil {
			fatalf("read objects: %v", err)
		}
		for _, row := range rows {
			fmt.Println(row)
		}
	case "query":
		if strings.TrimSpace(*sqlText) == "" {
			fatalf("query action requires -sql")
		}
		rows, err := queryRows(ctx, db, *sqlText)
		if err != nil {
			fatalf("query: %v", err)
		}
		for _, row := range rows {
			fmt.Println(row)
		}
	case "exec":
		if strings.TrimSpace(*sqlText) == "" {
			fatalf("exec action requires -sql")
		}
		if err := execSQL(ctx, db, *sqlText); err != nil {
			fatalf("exec: %v", err)
		}
	default:
		fatalf("unsupported action: %s", *action)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func resolveSchemas(ctx context.Context, db *sql.DB, driver, requested, single string) ([]string, error) {
	raw := requested
	if strings.TrimSpace(raw) == "" {
		raw = single
	}
	if strings.TrimSpace(raw) == "" {
		expr := "current_schema()"
		if driver == "mysql" {
			expr = "DATABASE()"
		}
		var schema sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT "+expr).Scan(&schema); err != nil {
			return nil, err
		}
		if !schema.Valid || strings.TrimSpace(schema.String) == "" {
			return nil, fmt.Errorf("database has no current schema")
		}
		return []string{strings.TrimSpace(schema.String)}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid schemas requested")
	}
	return out, nil
}

func quote(driver, ident string) string {
	if driver == "mysql" {
		return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func qualified(driver, schema, table string) string {
	parts := strings.Split(table, ".")
	if len(parts) > 1 {
		for i := range parts {
			parts[i] = quote(driver, parts[i])
		}
		return strings.Join(parts, ".")
	}
	return quote(driver, schema) + "." + quote(driver, table)
}

func resetDatabase(ctx context.Context, db *sql.DB, driver string, schemas []string) error {
	if driver == "mysql" {
		var current string
		var defaultCharset, defaultCollation string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(DATABASE(), ''),
       COALESCE(@@character_set_database, @@character_set_server),
       COALESCE(@@collation_database, @@collation_server)`).Scan(&current, &defaultCharset, &defaultCollation); err != nil {
			return err
		}
		for _, schema := range schemas {
			charset, collation := defaultCharset, defaultCollation
			var foundCharset, foundCollation string
			err := db.QueryRowContext(ctx, `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?`, schema).Scan(&foundCharset, &foundCollation)
			if err == nil {
				charset, collation = foundCharset, foundCollation
			} else if err != sql.ErrNoRows {
				return err
			}
			if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(driver, schema)); err != nil {
				return err
			}
			create := fmt.Sprintf("CREATE DATABASE %s CHARACTER SET %s COLLATE %s", quote(driver, schema), charset, collation)
			if _, err := db.ExecContext(ctx, create); err != nil {
				return err
			}
		}
		if current != "" {
			_, err := db.ExecContext(ctx, "USE "+quote(driver, current))
			return err
		}
		return nil
	}

	for _, schema := range schemas {
		stmts := []string{
			"DROP SCHEMA IF EXISTS " + quote(driver, schema) + " CASCADE",
			"CREATE SCHEMA " + quote(driver, schema),
		}
		for _, stmt := range stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
	}
	return nil
}

func historyRows(ctx context.Context, db *sql.DB, driver, schema, table string) ([]string, error) {
	exists, err := tableExists(ctx, db, driver, schema, table)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	q := fmt.Sprintf(`SELECT installed_rank, version, description, type, script, checksum,
       installed_by, success
FROM %s ORDER BY installed_rank`, qualified(driver, schema, table))
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var rank int
		var version, description, typ, script, installedBy sql.NullString
		var checksum sql.NullInt64
		var success any
		if err := rows.Scan(&rank, &version, &description, &typ, &script, &checksum, &installedBy, &success); err != nil {
			return nil, err
		}
		successText := "0"
		if successBool(success) {
			successText = "1"
		}
		out = append(out, fmt.Sprintf("HISTORY\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
			rank,
			nullString(version, true),
			sanitize(description.String),
			sanitize(typ.String),
			sanitize(script.String),
			nullInt(checksum),
			sanitize(installedBy.String),
			successText,
		))
	}
	return out, rows.Err()
}

func tableExists(ctx context.Context, db *sql.DB, driver, schema, table string) (bool, error) {
	var n int
	if driver == "mysql" {
		err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM information_schema.tables
WHERE table_schema = ? AND table_name = ?`, schema, table).Scan(&n)
		return n > 0, err
	}
	err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM information_schema.tables
WHERE table_schema = $1 AND table_name = $2`, schema, table).Scan(&n)
	return n > 0, err
}

func execSQL(ctx context.Context, db *sql.DB, sqlText string) error {
	_, err := db.ExecContext(ctx, sqlText)
	return err
}

func queryRows(ctx context.Context, db *sql.DB, sqlText string) ([]string, error) {
	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []string{"QUERY\t" + sanitize(sqlText)}
	for rows.Next() {
		values := make([]any, len(columns))
		scan := make([]any, len(columns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, err
		}
		parts := make([]string, 0, len(values)+1)
		parts = append(parts, "DATA")
		for _, value := range values {
			parts = append(parts, canonicalValue(value))
		}
		out = append(out, strings.Join(parts, "\t"))
	}
	return out, rows.Err()
}

func canonicalValue(value any) string {
	if value == nil {
		return "NULL"
	}
	switch v := value.(type) {
	case []byte:
		return sanitize(string(v))
	case string:
		return sanitize(v)
	default:
		return sanitize(fmt.Sprint(v))
	}
}

func objectRows(ctx context.Context, db *sql.DB, driver string, schemas []string, table string) ([]string, error) {
	var out []string
	for _, schema := range schemas {
		var rows []string
		var err error
		if driver == "mysql" {
			rows, err = mysqlObjects(ctx, db, schema, table)
		} else {
			rows, err = postgresObjects(ctx, db, schema, table)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	sort.Strings(out)
	return out, nil
}

func postgresObjects(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	q := `
WITH objects AS (
  SELECT 'schema'::text AS kind, n.nspname AS identity
  FROM pg_catalog.pg_namespace n
  WHERE n.nspname = $1
  UNION ALL
  SELECT 'foreign table'::text, n.nspname || '.' || c.relname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind = 'f'
  UNION ALL
  SELECT 'materialized view'::text, n.nspname || '.' || c.relname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind = 'm'
  UNION ALL
  SELECT 'sequence'::text, n.nspname || '.' || c.relname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind = 'S'
  UNION ALL
  SELECT 'table'::text, n.nspname || '.' || c.relname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind IN ('r','p')
    AND NOT (n.nspname = $1 AND c.relname = $2)
  UNION ALL
  SELECT 'view'::text, n.nspname || '.' || c.relname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind = 'v'
  UNION ALL
  SELECT CASE t.typtype
           WHEN 'e' THEN 'enum'
           WHEN 'd' THEN 'domain'
           WHEN 'c' THEN 'composite type'
           WHEN 'r' THEN 'range type'
           WHEN 'm' THEN 'multirange type'
           ELSE 'type'
         END,
         n.nspname || '.' || t.typname
  FROM pg_catalog.pg_type t
  JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = t.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL
    AND (t.typtype IN ('d','e','r','m')
      OR (t.typtype = 'c' AND EXISTS (
        SELECT 1 FROM pg_catalog.pg_class c
        WHERE c.oid = t.typrelid AND c.relkind = 'c'
      )))
  UNION ALL
  SELECT CASE p.prokind
           WHEN 'a' THEN 'aggregate'
           WHEN 'p' THEN 'procedure'
           ELSE 'function'
         END,
         n.nspname || '.' || p.proname || '(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')'
  FROM pg_catalog.pg_proc p
  JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
  WHERE n.nspname = $1 AND d.objid IS NULL AND p.prokind IN ('f','p','a')
  UNION ALL
  SELECT 'trigger'::text,
         tn.nspname || '.' || c.relname || '.' || tr.tgname
  FROM pg_catalog.pg_trigger tr
  JOIN pg_catalog.pg_class c ON c.oid = tr.tgrelid
  JOIN pg_catalog.pg_namespace tn ON tn.oid = c.relnamespace
  LEFT JOIN pg_catalog.pg_depend d ON d.objid = tr.oid AND d.deptype = 'e'
  WHERE tn.nspname = $1 AND NOT tr.tgisinternal AND d.objid IS NULL
)
SELECT kind, identity FROM objects ORDER BY kind, identity`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjects(rows)
}

func mysqlObjects(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	var out []string
	appendQuery := func(kind, q string, args ...any) error {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			if strings.EqualFold(name, tableBaseName(table)) {
				continue
			}
			out = append(out, fmt.Sprintf("OBJECT\t%s\t%s", kind, sanitize(name)))
		}
		return rows.Err()
	}

	queries := []struct {
		kind string
		q    string
	}{
		{"schema", `SELECT schema_name FROM information_schema.schemata WHERE schema_name = ?`},
		{"trigger", `SELECT trigger_name FROM information_schema.triggers WHERE trigger_schema = ?`},
		{"table", `SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE'`},
		{"view", `SELECT table_name FROM information_schema.views WHERE table_schema = ?`},
		{"procedure", `SELECT routine_name FROM information_schema.routines WHERE routine_schema = ? AND routine_type = 'PROCEDURE'`},
		{"function", `SELECT routine_name FROM information_schema.routines WHERE routine_schema = ? AND routine_type = 'FUNCTION'`},
		{"event", `SELECT event_name FROM information_schema.events WHERE event_schema = ?`},
		{"sequence", `SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'SEQUENCE'`},
	}
	for _, item := range queries {
		if err := appendQuery(item.kind, item.q, schema); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
func scanObjects(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var kind, identity string
		if err := rows.Scan(&kind, &identity); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("OBJECT\t%s\t%s", sanitize(kind), sanitize(identity)))
	}
	sort.Strings(out)
	return out, rows.Err()
}

func tableBaseName(table string) string {
	parts := strings.Split(table, ".")
	return parts[len(parts)-1]
}

func successBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int32:
		return x != 0
	case []byte:
		s := strings.ToLower(string(x))
		return s == "1" || s == "true" || s == "t"
	case string:
		s := strings.ToLower(x)
		return s == "1" || s == "true" || s == "t"
	default:
		return false
	}
}

func nullString(v sql.NullString, quotedNull bool) string {
	if !v.Valid {
		return "NULL"
	}
	return sanitize(v.String)
}

func nullInt(v sql.NullInt64) string {
	if !v.Valid {
		return "NULL"
	}
	return fmt.Sprintf("%d", v.Int64)
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
