package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SchemaHistoryRow 严格对应 Flyway 的 flyway_schema_history 表结构。
type SchemaHistoryRow struct {
	InstalledRank int
	Version       string
	VersionValid  bool
	Description   string
	Type          string
	Script        string
	Checksum      sql.NullInt64
	InstalledBy   string
	InstalledOn   time.Time
	ExecutionTime int
	Success       bool
}

// SchemaHistoryTable 封装历史表的读写。
type SchemaHistoryTable struct {
	db      *sql.DB
	dialect Dialect
	name    string
}

type dbExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// NewSchemaHistoryTable 构造历史表访问对象。
func NewSchemaHistoryTable(db *sql.DB, dialect Dialect, table string) *SchemaHistoryTable {
	return &SchemaHistoryTable{db: db, dialect: dialect, name: table}
}

// Name 返回历史表名。
func (h *SchemaHistoryTable) Name() string { return h.name }

func (h *SchemaHistoryTable) FullName() string { return h.name }

// QuotedName returns the SQL-qualified history table name used by CLI output.
func (h *SchemaHistoryTable) QuotedName() string { return h.quoted() }

func (h *SchemaHistoryTable) quoted() string { return quoteQualified(h.dialect, h.name) }

// Exists 判断历史表是否存在。只有明确的“表不存在”错误才返回 false，
// 权限、连接和结构错误必须向上抛出，不能伪装成空库。
func (h *SchemaHistoryTable) Exists(ctx context.Context) (bool, error) {
	return h.existsWithExecutor(ctx, h.db)
}

func (h *SchemaHistoryTable) existsWithExecutor(ctx context.Context, exec dbExecutor) (bool, error) {
	exists, err := h.dialect.HistoryTableExists(ctx, exec, h.name)
	if err != nil {
		return false, fmt.Errorf("检测历史表 %s 失败: %w", h.name, err)
	}
	return exists, nil
}

// Create 创建历史表，不写入 baseline。
func (h *SchemaHistoryTable) Create(ctx context.Context) error {
	return h.create(ctx, h.db, false, Version{}, "", "")
}

// CreateWithBaseline 创建历史表并原子地写入官方格式的 BASELINE 记录。
func (h *SchemaHistoryTable) CreateWithBaseline(ctx context.Context, version Version, description, installedBy string) error {
	return h.create(ctx, h.db, true, version, description, installedBy)
}

// CreateWithBaselineWithExecutor writes the history table and BASELINE marker
// on the caller's session so PostgreSQL timezone/search_path are preserved.
func (h *SchemaHistoryTable) CreateWithBaselineWithExecutor(ctx context.Context, exec dbExecutor, version Version, description, installedBy string) error {
	return h.create(ctx, exec, true, version, description, installedBy)
}

// CreateWithExecutor 在调用方提供的连接/事务中创建历史表。
func (h *SchemaHistoryTable) CreateWithExecutor(ctx context.Context, exec dbExecutor, baseline bool, version Version, description, installedBy string) error {
	return h.create(ctx, exec, baseline, version, description, installedBy)
}

// create does not retry internally. DbSchemas owns the outer ten-attempt retry
// loop, which must cover schema creation, history creation and the marker.
func (h *SchemaHistoryTable) create(ctx context.Context, exec dbExecutor, baseline bool, version Version, description, installedBy string) error {
	exists, err := h.existsWithExecutor(ctx, exec)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	statements := h.dialect.CreateSchemaHistoryTableStatements(h.name)
	for i, stmt := range statements {
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			// A concurrent creator may have completed the table between our
			// existence check and CREATE TABLE. Only the table statement is
			// safe to reconcile this way; constraint/index failures must be
			// surfaced because otherwise an incomplete history table would be
			// treated as successfully created.
			if i == 0 {
				exists, existsErr := h.existsWithExecutor(ctx, exec)
				if existsErr != nil {
					return fmt.Errorf("创建历史表 %s 失败: %w（无法确认表是否创建成功: %v）", h.name, err, existsErr)
				}
				if exists {
					continue
				}
			}
			return fmt.Errorf("创建历史表 %s 失败: %w", h.name, err)
		}
	}
	if baseline {
		exists, err := h.existsWithExecutor(ctx, exec)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("创建历史表 %s 后仍无法检测到该表", h.name)
		}
		marker, err := h.getBaselineMarkerWithExecutor(ctx, exec)
		if err != nil {
			return err
		}
		if marker != nil {
			return nil
		}
		return h.addBaseline(ctx, exec, version, description, installedBy)
	}
	return nil
}

// All 按 installed_rank 升序读取全部记录；历史表不存在时返回空列表。
func (h *SchemaHistoryTable) All(ctx context.Context) ([]SchemaHistoryRow, error) {
	return h.allWithExecutor(ctx, h.db)
}

// AllWithExecutor reads the history table on the caller's session.
func (h *SchemaHistoryTable) AllWithExecutor(ctx context.Context, exec dbExecutor) ([]SchemaHistoryRow, error) {
	return h.allWithExecutor(ctx, exec)
}

// AllWithExecutorKnownExists skips the existence probe when the caller has
// already checked it on the same session.
func (h *SchemaHistoryTable) AllWithExecutorKnownExists(ctx context.Context, exec dbExecutor, exists bool) ([]SchemaHistoryRow, error) {
	if !exists {
		return []SchemaHistoryRow{}, nil
	}
	return h.allWithExecutorKnownExists(ctx, exec)
}

func (h *SchemaHistoryTable) allWithExecutor(ctx context.Context, exec dbExecutor) ([]SchemaHistoryRow, error) {
	exists, err := h.existsWithExecutor(ctx, exec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []SchemaHistoryRow{}, nil
	}
	return h.allWithExecutorKnownExists(ctx, exec)
}

func (h *SchemaHistoryTable) allWithExecutorKnownExists(ctx context.Context, exec dbExecutor) ([]SchemaHistoryRow, error) {
	q := fmt.Sprintf(`SELECT installed_rank, version, description, type, script, checksum,
       installed_by, installed_on, execution_time, success
FROM %s ORDER BY installed_rank ASC`, h.quoted())
	rows, err := exec.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}).QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("读取历史表失败: %w", err)
	}
	defer rows.Close()

	var out []SchemaHistoryRow
	for rows.Next() {
		var r SchemaHistoryRow
		var version sql.NullString
		var installedOn any
		var success any
		if err := rows.Scan(&r.InstalledRank, &version, &r.Description, &r.Type, &r.Script,
			&r.Checksum, &r.InstalledBy, &installedOn, &r.ExecutionTime, &success); err != nil {
			return nil, fmt.Errorf("扫描历史表记录失败: %w", err)
		}
		r.Version = version.String
		r.VersionValid = version.Valid
		if version.Valid {
			// 官方 JdbcTableSchemaHistory.allAppliedMigrations() 在读取每行时
			// 立即 MigrationVersion.fromVersion()，因此非法版本（包括 baseline
			// 特殊版本的展示值）必须在读取阶段失败，而不能留给后续命令忽略。
			if _, perr := ParseVersion(version.String); perr != nil {
				return nil, fmt.Errorf("历史表版本号非法 %q: %w", version.String, perr)
			}
		}
		t, terr := parseDBTime(installedOn)
		if terr != nil {
			return nil, fmt.Errorf("解析历史表 installed_on 失败: %w", terr)
		}
		r.InstalledOn = t
		r.Success = parseDBSuccess(success)
		out = append(out, r)
	}
	return out, rows.Err()
}

func parseDBTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case string:
		return parseDBTimeString(x)
	case []byte:
		return parseDBTimeString(string(x))
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("不支持的时间类型 %T", v)
	}
}

func parseDBTimeString(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q", s)
}

func parseDBSuccess(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case []byte:
		return string(x) == "1" || strings.EqualFold(string(x), "true")
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	default:
		return false
	}
}

// AddApplied 插入一条成功执行记录。
func (h *SchemaHistoryTable) AddApplied(ctx context.Context, m *Migration, rank int, executionTime int, installedBy string) error {
	return h.addApplied(ctx, h.db, m, rank, executionTime, installedBy)
}

func (h *SchemaHistoryTable) addApplied(ctx context.Context, exec dbExecutor, m *Migration, rank int, executionTime int, installedBy string) error {
	return h.insert(ctx, exec, rank, m.Version, m.Description, m.Type(), m.Script, m.Checksum, executionTime, installedBy, true)
}

// AddAppliedTx 在迁移事务内写入成功记录，使 DDL 与历史记录同生共死。
func (h *SchemaHistoryTable) AddAppliedTx(ctx context.Context, tx *sql.Tx, m *Migration, rank int, executionTime int, installedBy string) error {
	return h.addApplied(ctx, tx, m, rank, executionTime, installedBy)
}

// AddFailed 记录一条失败记录（success = false）。
// 只有无法回滚 DDL 的数据库才应调用；PostgreSQL 的失败事务会整体回滚。
func (h *SchemaHistoryTable) AddFailed(ctx context.Context, m *Migration, rank int, executionTime int, installedBy string) error {
	return h.addFailed(ctx, h.db, m, rank, executionTime, installedBy)
}

func (h *SchemaHistoryTable) addFailed(ctx context.Context, exec dbExecutor, m *Migration, rank int, executionTime int, installedBy string) error {
	return h.insert(ctx, exec, rank, m.Version, m.Description, m.Type(), m.Script, m.Checksum, executionTime, installedBy, false)
}

// AddBaseline 写入官方格式的 BASELINE 记录：rank=1，checksum=NULL，
// script 为 baselineDescription 的缩写形式。
func (h *SchemaHistoryTable) AddBaseline(ctx context.Context, version Version, description, installedBy string) error {
	return h.addBaseline(ctx, h.db, version, description, installedBy)
}

func (h *SchemaHistoryTable) addBaseline(ctx context.Context, exec dbExecutor, version Version, description, installedBy string) error {
	return h.insert(ctx, exec, 1, version, description, TypeBaseline, description, nil, 0, installedBy, true)
}

// AddSchemasMarker 写入 Flyway 的 SCHEMA 合成记录。
func (h *SchemaHistoryTable) AddSchemasMarker(ctx context.Context, schemas []string, installedBy string) error {
	return h.AddSchemasMarkerWithExecutor(ctx, h.db, schemas, installedBy)
}

// AddSchemasMarkerWithExecutor writes the SCHEMA marker on the same connection
// or transaction used for schema/history creation.
func (h *SchemaHistoryTable) AddSchemasMarkerWithExecutor(ctx context.Context, exec dbExecutor, schemas []string, installedBy string) error {
	rank, err := h.nextRank(ctx, exec, TypeSchema)
	if err != nil {
		return err
	}
	quoted := make([]string, len(schemas))
	for i, schema := range schemas {
		quoted[i] = h.dialect.Quote(schema)
	}
	script := strings.Join(quoted, ",")
	return h.insert(ctx, exec, rank, Version{}, "<< Flyway Schema Creation >>", TypeSchema, script, nil, 0, installedBy, true)
}

// AddDelete 追加 DELETE 标记，而不是物理删除原迁移记录。
func (h *SchemaHistoryTable) AddDelete(ctx context.Context, applied SchemaHistoryRow, installedBy string) error {
	return h.AddDeleteWithExecutor(ctx, h.db, applied, installedBy)
}

// AddDeleteWithExecutor appends a DELETE marker using the caller's configured
// session. The rank lookup and insert must use that same session.
func (h *SchemaHistoryTable) AddDeleteWithExecutor(ctx context.Context, exec dbExecutor, applied SchemaHistoryRow, installedBy string) error {
	rank, err := h.nextRank(ctx, exec, applied.Type)
	if err != nil {
		return err
	}
	version := Version{}
	if applied.VersionValid {
		v, perr := ParseVersion(applied.Version)
		if perr != nil {
			return fmt.Errorf("历史记录版本非法 %q: %w", applied.Version, perr)
		}
		version = v
	}
	return h.insert(ctx, exec, rank, version, applied.Description, TypeDelete, applied.Script,
		nullableInt64Value(applied.Checksum), 0, installedBy, applied.Success)
}

func nullableInt64Value(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func (h *SchemaHistoryTable) insert(ctx context.Context, exec dbExecutor, rank int, version Version, description, typ, script string, checksum any, executionTime int, installedBy string, success bool) error {
	var versionVal any
	if !version.IsZero() && !version.IsEmpty() {
		// 官方历史表保留 << Latest Version >> / << Current Version >> / << Next Version >> 显示值。
		versionVal = version.String()
	}
	var successVal any
	if h.dialect.Name() == "mysql" {
		if success {
			successVal = 1
		} else {
			successVal = 0
		}
	} else {
		successVal = success
	}

	q := fmt.Sprintf(`INSERT INTO %s
(installed_rank, version, description, type, script, checksum, installed_by, execution_time, success)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)`, h.quoted(),
		h.placeholder(1), h.placeholder(2), h.placeholder(3), h.placeholder(4),
		h.placeholder(5), h.placeholder(6), h.placeholder(7), h.placeholder(8), h.placeholder(9))

	_, err := exec.ExecContext(ctx, q, rank, versionVal, abbreviateDescription(description), typ,
		abbreviateScript(script), checksum, installedBy, executionTime, successVal)
	if err != nil {
		return fmt.Errorf("写入历史表失败: %w", err)
	}
	return nil
}

func abbreviateDescription(s string) string {
	r := []rune(s)
	if len(r) <= 200 {
		return s
	}
	return string(r[:197]) + "..."
}

func abbreviateScript(s string) string {
	r := []rune(s)
	if len(r) <= 1000 {
		return s
	}
	return "..." + string(r[3:1000])
}

// NextRank 返回下一个可用的 installed_rank。
func (h *SchemaHistoryTable) NextRank(ctx context.Context) (int, error) {
	return h.nextRank(ctx, h.db, TypeSQLMigration)
}

func (h *SchemaHistoryTable) NextRankTx(ctx context.Context, tx *sql.Tx) (int, error) {
	return h.nextRank(ctx, tx, TypeSQLMigration)
}

// NextRankType 复刻 calculateInstalledRank(type)：空表 SCHEMA 为 0，其他为 1；
// 非空表始终是最后一条 installed_rank + 1，不能使用 MAX()。
func (h *SchemaHistoryTable) NextRankType(ctx context.Context, typ string) (int, error) {
	return h.nextRank(ctx, h.db, typ)
}

func (h *SchemaHistoryTable) NextRankTypeWithExecutor(ctx context.Context, exec dbExecutor, typ string) (int, error) {
	return h.nextRank(ctx, exec, typ)
}

func (h *SchemaHistoryTable) nextRank(ctx context.Context, exec dbExecutor, typ string) (int, error) {
	var last sql.NullInt64
	q := fmt.Sprintf("SELECT installed_rank FROM %s ORDER BY installed_rank DESC LIMIT 1", h.quoted())
	if err := exec.QueryRowContext(ctx, q).Scan(&last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if typ == TypeSchema {
				return 0, nil
			}
			return 1, nil
		}
		return 0, fmt.Errorf("读取最后一条 installed_rank 失败: %w", err)
	}
	return int(last.Int64) + 1, nil
}

// RemoveFailed 按官方规则物理删除失败记录：版本化按 version，可重复按 description。
func (h *SchemaHistoryTable) RemoveFailed(ctx context.Context) (int64, error) {
	return h.RemoveFailedWithExecutor(ctx, h.db)
}

// RemoveFailedWithExecutor removes failed history rows on the caller's session.
func (h *SchemaHistoryTable) RemoveFailedWithExecutor(ctx context.Context, exec dbExecutor) (int64, error) {
	applied, err := h.allWithExecutor(ctx, exec)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, row := range applied {
		if row.Success {
			continue
		}
		var res sql.Result
		if row.VersionValid {
			q := fmt.Sprintf("DELETE FROM %s WHERE success = %s AND version = %s", h.quoted(), h.boolFalse(), h.placeholder(1))
			res, err = exec.ExecContext(ctx, q, row.Version)
		} else {
			q := fmt.Sprintf("DELETE FROM %s WHERE success = %s AND version IS NULL AND description = %s", h.quoted(), h.boolFalse(), h.placeholder(1))
			res, err = exec.ExecContext(ctx, q, row.Description)
		}
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func (h *SchemaHistoryTable) boolFalse() string {
	if h.dialect.Name() == "mysql" {
		return "0"
	}
	return "false"
}

// UpdateApplied 精确复刻 repair 的 UPDATE：description/type/checksum WHERE installed_rank。
func (h *SchemaHistoryTable) UpdateApplied(ctx context.Context, applied SchemaHistoryRow, resolved *Migration) error {
	return h.UpdateAppliedWithExecutor(ctx, h.db, applied, resolved)
}

// UpdateAppliedWithExecutor applies repair's UPDATE on the caller's session.
func (h *SchemaHistoryTable) UpdateAppliedWithExecutor(ctx context.Context, exec dbExecutor, applied SchemaHistoryRow, resolved *Migration) error {
	description := resolved.Description
	checksum := resolved.Checksum
	typ := resolved.Type()
	if isSyntheticType(applied.Type) {
		typ = applied.Type
	}
	q := fmt.Sprintf("UPDATE %s SET description = %s, type = %s, checksum = %s WHERE installed_rank = %s",
		h.quoted(), h.placeholder(1), h.placeholder(2), h.placeholder(3), h.placeholder(4))
	_, err := exec.ExecContext(ctx, q, abbreviateDescription(description), typ, checksum, applied.InstalledRank)
	if err != nil {
		return fmt.Errorf("更新历史表记录失败: %w", err)
	}
	return nil
}

// GetBaselineMarker 只看前两条记录，只认精确的 BASELINE 类型。
func (h *SchemaHistoryTable) GetBaselineMarker(ctx context.Context) (*SchemaHistoryRow, error) {
	return h.GetBaselineMarkerWithExecutor(ctx, h.db)
}

func (h *SchemaHistoryTable) GetBaselineMarkerWithExecutor(ctx context.Context, exec dbExecutor) (*SchemaHistoryRow, error) {
	return h.getBaselineMarkerWithExecutor(ctx, exec)
}

func (h *SchemaHistoryTable) getBaselineMarkerWithExecutor(ctx context.Context, exec dbExecutor) (*SchemaHistoryRow, error) {
	rows, err := h.allWithExecutor(ctx, exec)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(rows) && i < 2; i++ {
		if strings.EqualFold(rows[i].Type, TypeBaseline) {
			row := rows[i]
			return &row, nil
		}
	}
	return nil, nil
}

func (h *SchemaHistoryTable) HasSchemasMarker(ctx context.Context) (bool, error) {
	return h.HasSchemasMarkerWithExecutor(ctx, h.db)
}

func (h *SchemaHistoryTable) HasSchemasMarkerWithExecutor(ctx context.Context, exec dbExecutor) (bool, error) {
	return h.hasSchemasMarkerWithExecutor(ctx, exec)
}

func (h *SchemaHistoryTable) hasSchemasMarkerWithExecutor(ctx context.Context, exec dbExecutor) (bool, error) {
	applied, err := h.allWithExecutor(ctx, exec)
	if err != nil {
		return false, err
	}
	for _, row := range applied {
		if strings.EqualFold(row.Type, TypeSchema) {
			return true, nil
		}
	}
	return false, nil
}

// GetSchemasCreatedByFlyway mirrors SchemaHistory.getSchemasCreatedByFlyway:
// every SCHEMA marker script is split with Java String.split(",") semantics and
// each token is unquoted through the dialect. The result is intentionally not
// deduplicated because clean iterates the marker list in order.
func (h *SchemaHistoryTable) GetSchemasCreatedByFlyway(ctx context.Context) ([]string, error) {
	return h.getSchemasCreatedByFlywayWithExecutor(ctx, h.db)
}

func (h *SchemaHistoryTable) getSchemasCreatedByFlywayWithExecutor(ctx context.Context, exec dbExecutor) ([]string, error) {
	applied, err := h.allWithExecutor(ctx, exec)
	if err != nil {
		return nil, err
	}
	hasMarker := false
	for _, row := range applied {
		if strings.EqualFold(row.Type, TypeSchema) {
			hasMarker = true
			break
		}
	}
	if !hasMarker {
		return []string{}, nil
	}
	out := make([]string, 0)
	for _, row := range applied {
		if !strings.EqualFold(row.Type, TypeSchema) {
			continue
		}
		for _, schema := range javaSplitComma(row.Script) {
			out = append(out, h.dialect.UnQuote(schema))
		}
	}
	return out, nil
}

// javaSplitComma implements the subset of java.lang.String.split(",") used by
// Flyway's schema marker parsing: trailing empty strings are discarded while
// internal empty fields are preserved.
func javaSplitComma(value string) []string {
	if value == "" {
		return []string{""}
	}
	parts := strings.Split(value, ",")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func (h *SchemaHistoryTable) HasNonSyntheticAppliedMigrations(ctx context.Context) (bool, error) {
	return h.HasNonSyntheticAppliedMigrationsWithExecutor(ctx, h.db)
}

// HasNonSyntheticAppliedMigrationsWithExecutor evaluates the same predicate on
// the caller's configured session.
func (h *SchemaHistoryTable) HasNonSyntheticAppliedMigrationsWithExecutor(ctx context.Context, exec dbExecutor) (bool, error) {
	applied, err := h.allWithExecutor(ctx, exec)
	if err != nil {
		return false, err
	}
	for _, row := range applied {
		if isSyntheticType(row.Type) || isUndoType(row.Type) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// DeleteAll 清空历史表（clean 用）。
func (h *SchemaHistoryTable) DeleteAll(ctx context.Context) error {
	_, err := h.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", h.quoted()))
	return err
}

func (h *SchemaHistoryTable) placeholder(n int) string {
	if h.dialect.Name() == "mysql" {
		return "?"
	}
	return fmt.Sprintf("$%d", n)
}

// CurrentUser 查询当前数据库用户，用于 installed_by 字段。
func CurrentUser(ctx context.Context, db *sql.DB, d Dialect) (string, error) {
	return currentUserWithExecutor(ctx, db, d)
}

func currentUserWithExecutor(ctx context.Context, exec dbExecutor, d Dialect) (string, error) {
	var u sql.NullString
	if err := exec.QueryRowContext(ctx, "SELECT "+d.CurrentUserExpr()).Scan(&u); err != nil {
		return "", err
	}
	return strings.TrimSpace(u.String), nil
}
