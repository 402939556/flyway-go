package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config 汇总一次运行所需的全部参数，命名与 Flyway 配置项保持一致。
type Config struct {
	Driver                  string   // postgres / mysql
	URL                     string   // flyway.url
	User                    string   // flyway.user
	Password                string   // flyway.password
	DSN                     string   // 数据源
	Locations               []string // 迁移脚本目录
	CallbackLocations       []string // SQL callback 目录
	JarDirs                 []string // flyway.jarDirs
	EnvironmentName         string   // Modern 当前环境
	DisplayName             string   // Modern 环境 displayName
	Provisioner             string   // Modern 环境 provisioner
	EnvironmentGroups       []string // Modern 环境 environmentGroups
	Resolvers               map[string]map[string]string
	DefaultSchema           string   // flyway.defaultSchema
	Table                   string   // 历史表名，默认 flyway_schema_history
	Schemas                 []string // 目标 schema，默认当前
	BaselineVersion         string   // baselineVersion，默认 1
	BaselineDesc            string   // baselineDescription
	BaselineOnMigrate       bool     // 库非空且无历史表时自动 baseline
	SkipExecutingMigrations bool     // flyway.skipExecutingMigrations
	OutOfOrder              bool
	IgnoreMissing           bool
	IgnorePending           bool
	IgnoreMigrationPatterns []string
	FailOnMissingLocations  *bool      // failOnMissingLocations，默认 false
	Mode                    ConfigMode // Classic 或 Modern 配置模型
	FailOnMissingTarget     bool
	CleanDisabled           *bool // cleanDisabled，默认 true
	ValidateOnMigrate       *bool // validateOnMigrate，默认 true
	Group                   bool
	Mixed                   *bool         // mixed，默认 false
	ExecuteInTransaction    *bool         // executeInTransaction，默认 true
	PlaceholderReplacement  *bool         // placeholderReplacement，默认 true
	CreateSchemas           *bool         // createSchemas，默认 true
	TargetVersion           string        // target，迁移到指定版本为止
	InstalledBy             string        // installedBy，空值回退当前数据库用户
	ConnectRetries          int           // connectRetries，默认 0
	ConnectRetriesInterval  *int          // connectRetriesInterval，默认 120 秒
	InitSQL                 string        // initSql
	Encoding                string        // encoding，默认 UTF-8
	DetectEncoding          *bool         // detectEncoding，默认 false
	SkipDefaultCallbacks    *bool         // skipDefaultCallbacks，默认 false
	LockRetryCount          *int          // 获取迁移锁的重试次数，默认 50
	LockRetryDelay          time.Duration // 获取迁移锁的重试间隔，默认 1s
	PGTransactionalLock     *bool         // PostgreSQL advisory lock in transaction, default true
	Naming                  NamingConfig
	ValidateMigrationNaming bool
	Placeholders            map[string]string
	PlaceholderPrefix       string
	PlaceholderSuffix       string
	PlaceholderSeparator    string
	ScriptPlaceholderPrefix string
	ScriptPlaceholderSuffix string
	JDBCProperties          map[string]string
	WorkingDirectory        string  // workingDirectory，仅影响默认 flyway.conf 加载与缺失目录提示
	Database                *sql.DB // 可选：外部注入连接

	// targetOptional records that the configured target ended in '?'. The
	// suffix is removed during normalization, so this state is required for
	// ApplyDefaults to remain idempotent when core.New applies it again.
	targetOptional   bool
	targetNormalized string
}

// Bool 返回配置项需要的 bool 指针，便于显式覆盖 Flyway 的默认值。
func Bool(v bool) *bool { return &v }

// ApplyDefaults fills the Flyway 13.13 OSS defaults. It intentionally remains
// idempotent because both the CLI and core constructors call it.
func (c *Config) ApplyDefaults() {
	switch c.Mode {
	case ConfigModeModern:
		c.ApplyDefaultsModern()
	default:
		c.ApplyDefaultsClassic()
	}
}

// ApplyDefaultsClassic mirrors ClassicConfiguration defaults.
// setDefaults is retained for internal tests and older callers.
func (c *Config) setDefaults() {
	c.ApplyDefaults()
}

func (c *Config) ApplyDefaultsClassic() {
	c.applyCommonDefaults()
	c.applyTargetDefaults()
}

// ApplyDefaultsModern mirrors FlywayModel.defaults() plus
// ClassicConfiguration.getTarget() semantics for the TOML/Modern model.
func (c *Config) ApplyDefaultsModern() {
	c.applyCommonDefaults()
	c.applyTargetDefaults()
}

// applyTargetDefaults mirrors ClassicConfiguration.getTarget(). A trailing '?'
// is the only way to opt out of failOnMissingTarget; every other value,
// including the default "latest", makes a missing target fatal.
func (c *Config) applyTargetDefaults() {
	target := strings.TrimSpace(c.TargetVersion)
	if target == "" {
		target = "latest"
	}
	if strings.HasSuffix(target, "?") {
		c.targetOptional = true
		target = strings.TrimSpace(strings.TrimSuffix(target, "?"))
		c.FailOnMissingTarget = false
	} else if !(c.targetOptional && target == c.targetNormalized) {
		// Remember the normalized value because the CLI and core.New both
		// apply defaults; a second call must not reinterpret "4" as the
		// non-optional spelling of the original "4?".
		c.targetOptional = false
		c.FailOnMissingTarget = true
	}
	c.TargetVersion = target
	c.targetNormalized = target
}

func (c *Config) applyCommonDefaults() {
	if len(c.Locations) == 0 {
		c.Locations = []string{"db/migration"}
	}
	if c.Table == "" {
		c.Table = "flyway_schema_history"
	}
	if c.BaselineVersion == "" {
		c.BaselineVersion = "1"
	}
	if c.BaselineDesc == "" {
		c.BaselineDesc = "<< Flyway Baseline >>"
	}
	defaults := DefaultNaming()
	if c.Naming.VersionedPrefix == "" {
		c.Naming.VersionedPrefix = defaults.VersionedPrefix
	}
	if c.Naming.UndoPrefix == "" {
		c.Naming.UndoPrefix = defaults.UndoPrefix
	}
	if c.Naming.RepeatablePrefix == "" {
		c.Naming.RepeatablePrefix = defaults.RepeatablePrefix
	}
	if c.Naming.BaselinePrefix == "" {
		c.Naming.BaselinePrefix = defaults.BaselinePrefix
	}
	if c.Naming.Separator == "" {
		c.Naming.Separator = defaults.Separator
	}
	if len(c.Naming.Suffixes) == 0 {
		c.Naming.Suffixes = append([]string(nil), defaults.Suffixes...)
	}
	if c.Encoding == "" && c.Naming.Encoding != "" {
		c.Encoding = c.Naming.Encoding
	}
	if c.Encoding == "" {
		c.Encoding = defaults.Encoding
	}
	c.Naming.Encoding = c.Encoding
	if c.CleanDisabled == nil {
		c.CleanDisabled = Bool(true)
	}
	if c.ValidateOnMigrate == nil {
		c.ValidateOnMigrate = Bool(true)
	}
	if c.Mixed == nil {
		c.Mixed = Bool(false)
	}
	if c.ExecuteInTransaction == nil {
		c.ExecuteInTransaction = Bool(true)
	}
	if c.PlaceholderReplacement == nil {
		c.PlaceholderReplacement = Bool(true)
	}
	if c.PGTransactionalLock == nil {
		c.PGTransactionalLock = Bool(true)
	}
	if c.CreateSchemas == nil {
		c.CreateSchemas = Bool(true)
	}
	if c.FailOnMissingLocations == nil {
		c.FailOnMissingLocations = Bool(false)
	}
	if c.DetectEncoding == nil {
		c.DetectEncoding = Bool(false)
	}
	if c.SkipDefaultCallbacks == nil {
		c.SkipDefaultCallbacks = Bool(false)
	}
	if c.PlaceholderPrefix == "" {
		c.PlaceholderPrefix = "${"
	}
	if c.PlaceholderSuffix == "" {
		c.PlaceholderSuffix = "}"
	}
	if c.PlaceholderSeparator == "" {
		c.PlaceholderSeparator = ":"
	}
	if c.ScriptPlaceholderPrefix == "" {
		c.ScriptPlaceholderPrefix = "FP__"
	}
	if c.ScriptPlaceholderSuffix == "" {
		c.ScriptPlaceholderSuffix = "__"
	}
	if len(c.IgnoreMigrationPatterns) == 0 {
		// FlywayModel's default is "*:future".
		c.IgnoreMigrationPatterns = []string{"*:future"}
	}
	if c.ConnectRetriesInterval == nil {
		value := 120
		c.ConnectRetriesInterval = &value
	}
	if c.LockRetryCount == nil {
		value := 50
		c.LockRetryCount = &value
	}
	if c.LockRetryDelay <= 0 {
		c.LockRetryDelay = time.Second
	}
}

// Flyway 是执行引擎。
type Flyway struct {
	cfg       Config
	db        *sql.DB
	dialect   Dialect
	history   *SchemaHistoryTable
	schemas   []string
	resolved  []*Migration
	out       Logger
	dbVersion int
}

// Logger 抽象输出，便于 CLI 与测试共用。
type Logger interface {
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
}

// New 构造执行引擎并完成扫描与连接准备。
func New(ctx context.Context, cfg Config, logger Logger) (*Flyway, error) {
	cfg.ApplyDefaults()
	dialect, err := GetDialect(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if cfg.TargetVersion != "" {
		if _, err := ParseVersion(cfg.TargetVersion); err != nil {
			return nil, fmt.Errorf("非法 target 版本 %q: %w", cfg.TargetVersion, err)
		}
	}
	db := cfg.Database
	if db == nil {
		return nil, fmt.Errorf("必须提供数据库连接")
	}
	prepared := &Flyway{cfg: cfg, db: db, dialect: dialect, out: logger}
	conn, err := prepared.openConn(ctx)
	if err != nil {
		return nil, err
	}

	defaultSchema, schemas, err := resolveSchemas(ctx, conn, dialect, cfg.Schemas, cfg.DefaultSchema)
	closeErr := conn.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("关闭初始化连接失败: %w", closeErr)
	}
	dbVersion := prepared.dbVersion
	historyName := cfg.Table
	if !strings.Contains(historyName, ".") {
		historyName = defaultSchema + "." + historyName
	}

	scanner := &Scanner{
		Naming:                 cfg.Naming,
		Driver:                 cfg.Driver,
		DBVersion:              dbVersion,
		Mixed:                  *cfg.Mixed,
		ExecuteInTransaction:   *cfg.ExecuteInTransaction,
		PlaceholderReplacement: *cfg.PlaceholderReplacement,
		CollectInvalid:         cfg.ValidateMigrationNaming,
		Placeholders:           cfg.Placeholders,
		PlaceholderPrefix:      cfg.PlaceholderPrefix,
		PlaceholderSuffix:      cfg.PlaceholderSuffix,
		PlaceholderSeparator:   cfg.PlaceholderSeparator,
		DetectEncoding:         *cfg.DetectEncoding,
		FailOnMissingLocations: *cfg.FailOnMissingLocations,
		WorkingDirectory:       cfg.WorkingDirectory,
	}
	var resolved []*Migration
	var invalidNames []InvalidMigrationName
	for _, loc := range normalizeLocations(cfg.Locations) {
		scanner.InvalidNames = nil
		ms, serr := scanner.Scan(loc)
		if serr != nil {
			return nil, serr
		}
		resolved = append(resolved, ms...)
		invalidNames = append(invalidNames, scanner.InvalidNames...)
	}
	if cfg.ValidateMigrationNaming && len(invalidNames) != 0 {
		var b strings.Builder
		b.WriteString("Invalid SQL filenames found:\r\n")
		for i, item := range invalidNames {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(item.Message)
		}
		return nil, fmt.Errorf("%s", b.String())
	}
	if err := checkResolvedDuplicates(resolved); err != nil {
		return nil, err
	}
	sortResolvedMigrations(resolved)

	fw := &Flyway{
		cfg:       cfg,
		db:        db,
		dialect:   dialect,
		history:   NewSchemaHistoryTable(db, dialect, historyName),
		schemas:   schemas,
		resolved:  resolved,
		out:       logger,
		dbVersion: dbVersion,
	}
	return fw, nil
}

// resolveSchemas mirrors SchemaHistoryFactory.prepareSchemas. The returned
// default schema is always the first entry in the ordered target-schema list,
// because session configuration uses schemas[0] for search_path/database.
// normalizeLocations mirrors Locations.processLocations for the filesystem
// plugin: sort by descriptor, discard duplicates, and discard sub-locations
// once their parent is present.
func normalizeLocations(raw []string) []string {
	type locationEntry struct {
		raw      string
		path     string
		prefix   string
		absolute bool
	}
	entries := make([]locationEntry, 0, len(raw))
	for _, descriptor := range raw {
		trimmed := strings.TrimSpace(descriptor)
		if trimmed == "" {
			continue
		}
		prefix := ""
		path := trimmed
		if len(trimmed) >= 2 && trimmed[1] == ':' && ((trimmed[0] >= 'a' && trimmed[0] <= 'z') || (trimmed[0] >= 'A' && trimmed[0] <= 'Z')) {
			// Windows drive path.
		} else if i := strings.IndexByte(trimmed, ':'); i >= 0 {
			prefix = strings.ToLower(trimmed[:i+1])
			path = trimmed[i+1:]
		}
		if prefix == "" {
			prefix = "classpath:"
		}
		entries = append(entries, locationEntry{raw: trimmed, path: path, prefix: prefix, absolute: filepath.IsAbs(path)})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].raw < entries[j].raw
	})
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		duplicate := false
		for _, existing := range out {
			if existing == entry.raw {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		isSub := false
		for _, existing := range entries {
			if existing.raw == entry.raw {
				continue
			}
			if existing.prefix != entry.prefix {
				continue
			}
			if strings.HasPrefix(entry.raw+string(filepath.Separator), existing.raw+string(filepath.Separator)) {
				// Only discard a child when the parent was already selected.
				for _, selected := range out {
					if selected == existing.raw {
						isSub = true
						break
					}
				}
				if isSub {
					break
				}
			}
		}
		if !isSub {
			out = append(out, entry.raw)
		}
	}
	return out
}

// installedBy mirrors Flyway's installedBy fallback: explicit configuration
// wins, otherwise the database current user is used, with a stable final
// fallback when the user cannot be determined.
func (f *Flyway) installedBy(ctx context.Context, exec dbExecutor) string {
	if value := strings.TrimSpace(f.cfg.InstalledBy); value != "" {
		return value
	}
	if user, err := currentUserWithExecutor(ctx, exec, f.dialect); err == nil {
		if value := strings.TrimSpace(user); value != "" {
			return value
		}
	}
	return "flyway-go"
}
func resolveSchemas(ctx context.Context, exec dbExecutor, dialect Dialect, configured []string, defaultSchema string) (string, []string, error) {
	if strings.TrimSpace(defaultSchema) == "" && len(configured) == 0 {
		var current sql.NullString
		if err := exec.QueryRowContext(ctx, "SELECT "+dialect.CurrentSchemaExpr()).Scan(&current); err != nil {
			return "", nil, fmt.Errorf("获取当前 schema 失败: %w", err)
		}
		return resolveSchemaNames(configured, defaultSchema, current.String, nil)
	}
	return resolveSchemaNames(configured, defaultSchema, "", nil)
}

func resolveSchemaNames(configured []string, defaultSchema, currentSchema string, currentSchemaErr error) (string, []string, error) {
	defaultSchema = strings.TrimSpace(defaultSchema)
	names := make([]string, 0, len(configured)+1)
	for _, schema := range configured {
		schema = strings.TrimSpace(schema)
		if schema != "" {
			names = append(names, schema)
		}
	}

	if defaultSchema == "" {
		if len(names) > 0 {
			defaultSchema = names[0]
		} else {
			if currentSchemaErr != nil {
				return "", nil, currentSchemaErr
			}
			currentSchema = strings.TrimSpace(currentSchema)
			if currentSchema == "" {
				return "", nil, fmt.Errorf("Unable to determine schema for the schema history table. Set a default schema for the connection or specify one using the 'defaultSchema' property")
			}
			defaultSchema = currentSchema
		}
	}

	found := false
	for _, schema := range names {
		if schema == defaultSchema {
			found = true
			break
		}
	}
	if !found {
		names = append([]string{defaultSchema}, names...)
	}
	return defaultSchema, names, nil
}

func sortResolvedMigrations(ms []*Migration) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		return compareResolvedValues(a.Kind, a.Version, a.Description,
			b.Kind, b.Version, b.Description) < 0
	})
}

func checkResolvedDuplicates(ms []*Migration) error {
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			a, b := ms[i], ms[j]
			if !canCompareResolvedKinds(a.Kind, b.Kind) ||
				compareResolvedValues(a.Kind, a.Version, a.Description, b.Kind, b.Version, b.Description) != 0 {
				continue
			}
			return resolvedDuplicateError(a.Kind, a.Version, a.Description, a.Script, a.Path,
				b.Kind, b.Version, b.Description, b.Script, b.Path)
		}
	}
	return nil
}
func (f *Flyway) log(format string, args ...interface{}) {
	if f.out != nil {
		f.out.Info(format, args...)
	}
}

func (f *Flyway) warn(format string, args ...interface{}) {
	if f.out != nil {
		f.out.Warn(format, args...)
	}
}

// ---------------------------------------------------------------- shared helpers

func (f *Flyway) ignorePatterns() ([]ValidatePattern, error) {
	patterns := append([]string(nil), f.cfg.IgnoreMigrationPatterns...)
	if f.cfg.IgnoreMissing {
		patterns = append(patterns, "*:missing")
	}
	if f.cfg.IgnorePending {
		patterns = append(patterns, "*:pending")
	}
	return ParseIgnorePatterns(patterns)
}

func (f *Flyway) targetVersion() (*Version, error) {
	if f.cfg.TargetVersion == "" {
		return nil, nil
	}
	v, err := ParseVersion(f.cfg.TargetVersion)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (f *Flyway) targetSchemas(ctx context.Context) ([]string, error) {
	_ = ctx
	return append([]string(nil), f.schemas...), nil
}

func (f *Flyway) anySchemaNonEmpty(ctx context.Context, schemas []string) (bool, error) {
	conn, err := f.commandConn(ctx, "schema 检查")
	if err != nil {
		return false, err
	}
	defer conn.Close()
	return f.anySchemaNonEmptyWithExecutor(ctx, conn, schemas)
}

func (f *Flyway) anySchemaNonEmptyWithExecutor(ctx context.Context, exec dbExecutor, schemas []string) (bool, error) {
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
			return true, nil
		}
	}
	return false, nil
}

// createSchemas 复刻 DbSchemas.create：外层重试包住整个 schema 创建 attempt。
func (f *Flyway) createSchemas(ctx context.Context, schemas []string, baseline bool) error {
	if err := f.runCallbacks(ctx, EventCreateSchema); err != nil {
		return err
	}
	if err := f.runCallbacks(ctx, EventBeforeCreateSchema); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		err := f.createSchemasAttempt(ctx, schemas, baseline)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < 9 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("创建 schema 失败: %w", lastErr)
}

// createSchemasAttempt mirrors ExecutionTemplateFactory: PostgreSQL uses one
// transactional execution for schema creation, history creation, baseline and
// SCHEMA marker; MySQL uses a plain execution because DDL is not transactional.
func (f *Flyway) createSchemasAttempt(ctx context.Context, schemas []string, baseline bool) error {
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取 schema 创建连接失败: %w", err)
	}
	defer conn.Close()

	// PostgreSQL must use the JVM-equivalent session timezone before creating
	// history and baseline rows. Do not run the full schema configuration here:
	// MySQL's USE target_schema would fail while createSchemas is creating a
	// missing database, and PostgreSQL deliberately tolerates a missing schema.
	if err := f.configureSessionTimeZoneWithExecutor(ctx, conn); err != nil {
		return err
	}

	if f.dialect.SupportsDDLTransactions() && *f.cfg.ExecuteInTransaction {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("启动 schema 创建事务失败: %w", err)
		}
		if err := f.createSchemasWithExecutor(ctx, schemas, baseline, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交 schema 创建事务失败: %w", err)
		}
		return nil
	}
	return f.createSchemasWithExecutor(ctx, schemas, baseline, conn)
}

func (f *Flyway) createSchemasWithExecutor(ctx context.Context, schemas []string, baseline bool, exec dbExecutor) error {
	created, err := f.createMissingSchemasWithExecutor(ctx, schemas, exec)
	if err != nil {
		return err
	}
	if len(created) == 0 {
		return nil
	}
	if err := f.configureSessionSchemaWithExecutor(ctx, exec); err != nil {
		return err
	}
	installedBy := f.installedBy(ctx, exec)
	version := Version{}
	if baseline {
		v, verr := ParseVersion(f.cfg.BaselineVersion)
		if verr != nil {
			return fmt.Errorf("非法 baselineVersion %q: %w", f.cfg.BaselineVersion, verr)
		}
		version = v
	}
	if err := f.history.CreateWithExecutor(ctx, exec, baseline, version, f.cfg.BaselineDesc, installedBy); err != nil {
		return err
	}
	return f.history.AddSchemasMarkerWithExecutor(ctx, exec, created, installedBy)
}

func (f *Flyway) createMissingSchemasWithExecutor(ctx context.Context, schemas []string, exec dbExecutor) ([]string, error) {
	var created []string
	for _, schema := range schemas {
		ok, err := f.dialect.SchemaExists(ctx, exec, schema)
		if err != nil {
			return nil, fmt.Errorf("检查 schema %s 是否存在失败: %w", schema, err)
		}
		if ok {
			continue
		}
		if strings.TrimSpace(schema) == "" {
			return nil, fmt.Errorf("Unable to determine schema for the schema history table. Set a default schema for the connection or specify one using the defaultSchema property!")
		}
		if _, err := exec.ExecContext(ctx, f.dialect.CreateSchemaDDL(schema)); err != nil {
			return nil, fmt.Errorf("创建 schema %s 失败: %w", schema, err)
		}
		created = append(created, schema)
	}
	return created, nil
}

// migrateContext refreshes migration infos using official validateOnMigrate semantics.
func (f *Flyway) migrateContext(ctx context.Context, validate bool, extraIgnore []string) ([]*MigrationInfo, *MigrationInfoContext, error) {
	conn, err := f.commandConn(ctx, "迁移状态读取")
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	return f.migrateContextWithExecutor(ctx, conn, validate, extraIgnore)
}

func (f *Flyway) migrateContextWithExecutor(ctx context.Context, exec dbExecutor, validate bool, extraIgnore []string) ([]*MigrationInfo, *MigrationInfoContext, error) {
	patterns, err := f.ignorePatterns()
	if err != nil {
		return nil, nil, err
	}
	for _, raw := range extraIgnore {
		p, perr := ParseValidatePattern(raw)
		if perr != nil {
			return nil, nil, perr
		}
		patterns = append(patterns, p)
	}
	target, err := f.targetVersion()
	if err != nil {
		return nil, nil, err
	}
	applied, err := f.history.AllWithExecutor(ctx, exec)
	if err != nil {
		return nil, nil, err
	}
	baselineVersion := baselineVersionFromApplied(applied)
	infos, icontext, err := BuildMigrationInfos(f.resolved, applied, target, f.cfg.OutOfOrder, patterns, f.cfg.FailOnMissingTarget, baselineVersion)
	if err != nil {
		return nil, nil, err
	}
	if validate {
		var errs []error
		for _, info := range infos {
			if verr := info.Validate(); verr != nil {
				errs = append(errs, verr)
			}
		}
		if len(errs) != 0 {
			return nil, nil, aggregateValidateErrors(errs)
		}
	}
	return infos, icontext, nil
}

func aggregateValidateErrors(errs []error) error {
	var b strings.Builder
	b.WriteString("Validate failed: Migrations have failed validation\n")
	for _, err := range errs {
		b.WriteString(err.Error())
		b.WriteString("\n")
	}
	return fmt.Errorf("%s", strings.TrimSuffix(b.String(), "\n"))
}

// ---------------------------------------------------------------- migrate

// Migrate 复刻官方 Flyway.migrate 顶层流程。
func (f *Flyway) Migrate(ctx context.Context) error {
	if *f.cfg.ValidateOnMigrate {
		if _, _, err := f.validateForCommand(ctx, false, "*:pending"); err != nil {
			return err
		}
	}

	schemas, err := f.targetSchemas(ctx)
	if err != nil {
		return err
	}
	if *f.cfg.CreateSchemas {
		if err := f.createSchemas(ctx, schemas, false); err != nil {
			return err
		}
	}

	conn, err := f.commandConn(ctx, "migrate 初始化")
	if err != nil {
		return err
	}
	defer conn.Close()

	if !*f.cfg.CreateSchemas {
		for _, schema := range schemas {
			exists, existsErr := f.dialect.SchemaExists(ctx, conn, schema)
			if existsErr != nil {
				return fmt.Errorf("检查 schema %s 是否存在失败: %w", schema, existsErr)
			}
			if !exists {
				f.warn("Schema %s does not exist and createSchemas is false", schema)
			}
		}
	}

	exists, err := f.history.existsWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if !exists {
		nonEmpty, eerr := f.anySchemaNonEmptyWithExecutor(ctx, conn, schemas)
		if eerr != nil {
			return eerr
		}
		if nonEmpty {
			if !f.cfg.BaselineOnMigrate {
				quoted := make([]string, len(schemas))
				for i, schema := range schemas {
					quoted[i] = f.dialect.Quote(schema)
				}
				return fmt.Errorf("Found non-empty schema(s) %s but no schema history table. "+
					"Use baseline() or set baselineOnMigrate to true to initialize the schema history table",
					strings.Join(quoted, ","))
			}
			f.log("数据库非空且无历史表，自动执行 baseline（版本 %s）", f.cfg.BaselineVersion)
			bv, berr := ParseVersion(f.cfg.BaselineVersion)
			if berr != nil {
				return fmt.Errorf("非法 baselineVersion %q: %w", f.cfg.BaselineVersion, berr)
			}
			installedBy := f.installedBy(ctx, conn)
			if cerr := f.history.CreateWithBaselineWithExecutor(ctx, conn, bv, f.cfg.BaselineDesc, installedBy); cerr != nil {
				return cerr
			}
		} else {
			if cerr := f.history.CreateWithExecutor(ctx, conn, false, Version{}, f.cfg.BaselineDesc, f.installedBy(ctx, conn)); cerr != nil {
				return cerr
			}
		}
	}

	if err := f.runCallbacks(ctx, EventBeforeMigrate); err != nil {
		return err
	}

	firstRun := true
	total := 0
	isPreviousVersioned := true
	for {
		count, err := f.migrateGroup(ctx, firstRun, &isPreviousVersioned)
		if err != nil {
			_ = f.runCallbacks(ctx, EventAfterMigrateError)
			return err
		}
		total += count
		firstRun = false
		if count == 0 {
			break
		}
		if tv, terr := f.targetVersion(); terr == nil && tv != nil && tv.predef == predefNext { // next target applies one migration per migrate call
			break
		}
	}
	if isPreviousVersioned {
		if err := f.runCallbacks(ctx, EventAfterVersioned); err != nil {
			return err
		}
	}
	if total > 0 {
		if err := f.runCallbacks(ctx, EventAfterMigrateApplied); err != nil {
			return err
		}
	}
	if err := f.runCallbacks(ctx, EventAfterMigrate); err != nil {
		return err
	}
	return nil
}

// Event mirrors org.flywaydb.core.api.callback.Event.  The order is the enum
// declaration order and determines SQL callback execution order.
type Event int

const (
	EventBeforeClean Event = iota
	EventAfterClean
	EventAfterCleanError
	EventBeforeMigrate
	EventBeforeEachMigrate
	EventBeforeEachMigrateStatement
	EventAfterEachMigrateStatement
	EventAfterEachMigrateStatementError
	EventAfterEachMigrate
	EventAfterEachMigrateError
	EventBeforeRepeatables
	EventAfterVersioned
	EventAfterMigrateApplied
	EventAfterMigrate
	EventAfterMigrateError
	EventBeforeUndo
	EventBeforeEachUndo
	EventBeforeEachUndoStatement
	EventAfterEachUndoStatement
	EventAfterEachUndoStatementError
	EventAfterEachUndo
	EventAfterEachUndoError
	EventAfterUndo
	EventAfterUndoError
	EventBeforeValidate
	EventAfterValidate
	EventAfterValidateError
	EventBeforeBaseline
	EventAfterBaseline
	EventAfterBaselineError
	EventBeforeRepair
	EventAfterRepair
	EventAfterRepairError
	EventBeforeInfo
	EventAfterInfo
	EventAfterInfoError
	EventAfterMigrateOperationFinish
	EventAfterInfoOperationFinish
	EventAfterCleanOperationFinish
	EventAfterValidateOperationFinish
	EventAfterUndoOperationFinish
	EventAfterRepairOperationFinish
	EventAfterBaselineOperationFinish
	EventAfterReportGenerated
	EventCreateSchema
	EventBeforeCreateSchema
	EventBeforeConnect
	EventAfterConnect
)

func eventFromID(id string) (Event, bool) {
	for e := EventBeforeClean; e <= EventAfterConnect; e++ {
		if e.String() == id {
			return e, true
		}
	}
	return 0, false
}

func (e Event) String() string {
	switch e {
	case EventBeforeClean:
		return "beforeClean"
	case EventAfterClean:
		return "afterClean"
	case EventAfterCleanError:
		return "afterCleanError"
	case EventBeforeMigrate:
		return "beforeMigrate"
	case EventBeforeEachMigrate:
		return "beforeEachMigrate"
	case EventBeforeEachMigrateStatement:
		return "beforeEachMigrateStatement"
	case EventAfterEachMigrateStatement:
		return "afterEachMigrateStatement"
	case EventAfterEachMigrateStatementError:
		return "afterEachMigrateStatementError"
	case EventAfterEachMigrate:
		return "afterEachMigrate"
	case EventAfterEachMigrateError:
		return "afterEachMigrateError"
	case EventBeforeRepeatables:
		return "beforeRepeatables"
	case EventAfterVersioned:
		return "afterVersioned"
	case EventAfterMigrateApplied:
		return "afterMigrateApplied"
	case EventAfterMigrate:
		return "afterMigrate"
	case EventAfterMigrateError:
		return "afterMigrateError"
	case EventBeforeUndo:
		return "beforeUndo"
	case EventBeforeEachUndo:
		return "beforeEachUndo"
	case EventBeforeEachUndoStatement:
		return "beforeEachUndoStatement"
	case EventAfterEachUndoStatement:
		return "afterEachUndoStatement"
	case EventAfterEachUndoStatementError:
		return "afterEachUndoStatementError"
	case EventAfterEachUndo:
		return "afterEachUndo"
	case EventAfterEachUndoError:
		return "afterEachUndoError"
	case EventAfterUndo:
		return "afterUndo"
	case EventAfterUndoError:
		return "afterUndoError"
	case EventBeforeValidate:
		return "beforeValidate"
	case EventAfterValidate:
		return "afterValidate"
	case EventAfterValidateError:
		return "afterValidateError"
	case EventBeforeBaseline:
		return "beforeBaseline"
	case EventAfterBaseline:
		return "afterBaseline"
	case EventAfterBaselineError:
		return "afterBaselineError"
	case EventBeforeRepair:
		return "beforeRepair"
	case EventAfterRepair:
		return "afterRepair"
	case EventAfterRepairError:
		return "afterRepairError"
	case EventBeforeInfo:
		return "beforeInfo"
	case EventAfterInfo:
		return "afterInfo"
	case EventAfterInfoError:
		return "afterInfoError"
	case EventAfterMigrateOperationFinish:
		return "afterMigrateOperationFinish"
	case EventAfterInfoOperationFinish:
		return "afterInfoOperationFinish"
	case EventAfterCleanOperationFinish:
		return "afterCleanOperationFinish"
	case EventAfterValidateOperationFinish:
		return "afterValidateOperationFinish"
	case EventAfterUndoOperationFinish:
		return "afterUndoOperationFinish"
	case EventAfterRepairOperationFinish:
		return "afterRepairOperationFinish"
	case EventAfterBaselineOperationFinish:
		return "afterBaselineOperationFinish"
	case EventAfterReportGenerated:
		return "afterReportGenerated"
	case EventCreateSchema:
		return "createSchema"
	case EventBeforeCreateSchema:
		return "beforeCreateSchema"
	case EventBeforeConnect:
		return "beforeConnect"
	case EventAfterConnect:
		return "afterConnect"
	default:
		return ""
	}
}

// callbackScript corresponds to SqlScriptCallbackFactory.SqlScriptCallback.
type callbackScript struct {
	Event                  Event
	Description            string
	Script                 string
	SQL                    []string
	Parsed                 []ParsedStatement
	ExecuteInTransaction   *bool
	PlaceholderReplacement *bool
	PlaceholderError       string
}

// loadCallbacks implements SqlScriptCallbackFactory resource discovery. Callback
// resources use the same suffix and prefix/version/description parser as normal
// migrations; only names whose prefix is a callback Event are retained.
func (f *Flyway) loadCallbacks() ([]callbackScript, error) {
	if f.cfg.SkipDefaultCallbacks != nil && *f.cfg.SkipDefaultCallbacks {
		// This project currently implements only the default SQL callback
		// factory. Script callbacks (arbitrary .sh/.py resources) are resolved by
		// ScriptMigrationResolver even when skipDefaultCallbacks is true, so that
		// future implementation must remain outside this branch.
		return nil, nil
	}

	explicitCallbackLocations := len(f.cfg.CallbackLocations) > 0
	locations := f.cfg.CallbackLocations
	if !explicitCallbackLocations {
		locations = f.cfg.Locations
	}
	suffixes := f.cfg.Naming.Suffixes
	type callbackResource struct {
		name string
		path string
	}
	type found struct {
		name string
		path string
		cb   callbackScript
	}
	var resources []callbackResource
	for _, loc := range normalizeLocations(locations) {
		dir, err := filesystemLocationPath(loc)
		if err != nil {
			return nil, err
		}
		status := directoryValidationResult(dir)
		if status != "valid" {
			// FlywayExecutor.filterOutNonExistentCallbackLocations explicitly
			// skips missing callback locations. Fallback to the migration resource
			// provider still observes failOnMissingLocations.
			if explicitCallbackLocations {
				continue
			}
			if f.cfg.FailOnMissingLocations != nil && *f.cfg.FailOnMissingLocations {
				hint := (&Scanner{WorkingDirectory: f.cfg.WorkingDirectory}).workingDirectoryHint(dir)
				return nil, fmt.Errorf("Failed to find filesystem location: %s (%s)%s", dir, status, hint)
			}
			continue
		}
		walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErrIn error) error {
			if walkErrIn != nil {
				if f.cfg.FailOnMissingLocations != nil && *f.cfg.FailOnMissingLocations {
					return fmt.Errorf("Failed to find filesystem location: %s (unable to access folder)", path)
				}
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if path != dir && isHiddenDirectory(path, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !hasAnySuffix(name, suffixes) || !isReadableEntry(path, d) {
				return nil
			}
			resources = append(resources, callbackResource{name: name, path: path})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	var out []found
	byName := map[string]int{}
	for _, resource := range resources {
		name := resource.name
		stem := stripAnySuffix(name, suffixes)
		if stem == "" {
			continue
		}
		sep := f.cfg.Naming.Separator
		idx := strings.Index(stem, sep)
		prefix := stem
		description := ""
		if idx >= 0 {
			prefix = stem[:idx]
			description = stem[idx+len(sep):]
		}
		event, ok := eventFromID(prefix)
		if !ok {
			continue
		}
		md, merr := LoadSQLScriptMetadataWithEncoding(MigrationMetadataPath(resource.path), f.cfg.Placeholders,
			f.cfg.PlaceholderPrefix, f.cfg.PlaceholderSuffix, f.cfg.PlaceholderSeparator, f.cfg.Naming.Encoding)
		if merr != nil {
			return nil, fmt.Errorf("解析 callback %s 的脚本配置失败: %w", resource.path, merr)
		}
		raw, rerr := os.ReadFile(resource.path)
		if rerr != nil {
			if os.IsPermission(rerr) {
				continue
			}
			return nil, fmt.Errorf("读取 callback %s 失败: %w", resource.path, rerr)
		}
		detectEncoding := f.cfg.DetectEncoding != nil && *f.cfg.DetectEncoding
		if md.Encoding != "" {
			detectEncoding = false
		}
		decoded, decodeErr := DecodeScript(raw, encodingForMetadata(f.cfg.Naming.Encoding, md.Encoding), detectEncoding)
		if decodeErr != nil {
			return nil, fmt.Errorf("Failed to read %s: %w", name, decodeErr)
		}
		content := strings.TrimPrefix(decoded, "\uFEFF")
		placeholderReplacement := effectivePlaceholderReplacement(
			*f.cfg.PlaceholderReplacement, md.PlaceholderReplacement)
		executionContent := content
		var placeholderErr error
		if placeholderReplacement {
			executionContent, placeholderErr = ReplacePlaceholders(
				content, f.cfg.Placeholders, f.cfg.PlaceholderPrefix, f.cfg.PlaceholderSuffix, f.cfg.PlaceholderSeparator)
			if placeholderErr != nil {
				// Match SqlMigrationResolver: keep the callback resolvable and
				// report the placeholder failure when the callback is executed.
				executionContent = content
			}
		}
		parsed, serr := ParseSQLScript(executionContent, f.dialect.Name(), resource.path, f.dbVersion)
		if serr != nil {
			return nil, fmt.Errorf("解析 callback %s 失败: %w", resource.path, serr)
		}
		if err := validateMixedStatementKinds(parsed, *f.cfg.Mixed, md.ExecuteInTransaction,
			*f.cfg.ExecuteInTransaction); err != nil {
			return nil, fmt.Errorf("解析 callback %s 失败: %w", resource.path, err)
		}
		stmts := make([]string, 0, len(parsed))
		for _, st := range parsed {
			stmts = append(stmts, st.SQL)
		}
		cb := callbackScript{
			Event:                  event,
			Description:            strings.ReplaceAll(description, "_", " "),
			Script:                 name,
			SQL:                    stmts,
			Parsed:                 parsed,
			ExecuteInTransaction:   md.ExecuteInTransaction,
			PlaceholderReplacement: md.PlaceholderReplacement,
			PlaceholderError: func() string {
				if placeholderErr != nil {
					return placeholderErr.Error()
				}
				return ""
			}(),
		}
		if i, exists := byName[stem]; exists {
			return nil, fmt.Errorf("Found more than 1 SQL callback script called %s!\nOffenders:\n-> %s\n-> %s",
				stem, out[i].path, resource.path)
		}
		byName[stem] = len(out)
		out = append(out, found{name: stem, path: resource.path, cb: cb})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].cb, out[j].cb
		if a.Event != b.Event {
			return a.Event < b.Event
		}
		if a.Description != b.Description {
			if a.Description == "" {
				return true
			}
			if b.Description == "" {
				return false
			}
			return a.Description < b.Description
		}
		return a.Script < b.Script
	})
	callbacks := make([]callbackScript, 0, len(out))
	for _, item := range out {
		callbacks = append(callbacks, item.cb)
	}
	return callbacks, nil
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

func stripAnySuffix(name string, suffixes []string) string {
	for _, suffix := range suffixes {
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}

// runCallbacks executes every SQL callback registered for the event on a
// dedicated event connection. Flyway creates a separate execution template per
// callback, so each callback commits or rolls back independently.
func (f *Flyway) runCallbacks(ctx context.Context, event Event) error {
	callbacks, err := f.loadCallbacks()
	if err != nil {
		return err
	}
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取 callback 连接失败: %w", err)
	}
	defer conn.Close()

	for _, cb := range callbacks {
		if cb.Event != event {
			continue
		}
		if err := f.configureSessionSchema(ctx, conn); err != nil {
			return err
		}
		f.log("Executing SQL callback: %s", callbackLogName(event, cb))
		useTx := *f.cfg.ExecuteInTransaction && callbackScriptExecuteInTransaction(cb)
		if !useTx {
			if err := f.executeCallbackScript(ctx, event, cb, conn); err != nil {
				return err
			}
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开启 callback %s 事务失败: %w", cb.Script, err)
		}
		if err := f.executeCallbackScript(ctx, event, cb, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交 callback %s 事务失败: %w", cb.Script, err)
		}
	}
	return nil
}

func (f *Flyway) ensureSchemas(ctx context.Context, schemas []string) error {
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取 schema 创建连接失败: %w", err)
	}
	defer conn.Close()
	if err := f.configureSessionTimeZoneWithExecutor(ctx, conn); err != nil {
		return err
	}
	_, err = f.createMissingSchemasWithExecutor(ctx, schemas, conn)
	return err
}

func (f *Flyway) migrateGroup(ctx context.Context, firstRun bool, isPreviousVersioned *bool) (int, error) {
	lock, err := f.acquireMigrationLock(ctx)
	if err != nil {
		return 0, err
	}
	defer lock.Release()

	conn, err := f.commandConn(ctx, "迁移分组读取")
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	var ignore []string
	if !firstRun {
		ignore = []string{"*:*"}
	}
	infos, _, err := f.migrateContextWithExecutor(ctx, conn, false, ignore)
	if err != nil {
		return 0, err
	}
	// group=false official behavior: execute only the first pending migration.
	var pending []*MigrationInfo
	if f.cfg.Group {
		for _, info := range infos {
			if info.State() != StatePending || info.Resolved == nil || info.Resolved.Kind == KindUndo {
				continue
			}
			pending = append(pending, info)
		}
	} else {
		for _, info := range infos {
			if info.State() == StatePending && info.Resolved != nil && info.Resolved.Kind != KindUndo {
				pending = []*MigrationInfo{info}
				break
			}
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}
	installedBy := f.installedBy(ctx, conn)
	groupInSharedTx, planErr := f.groupExecutionPlan(pending)
	if planErr != nil {
		// Official Flyway performs this check before entering applyMigrations, so
		// afterEachMigrateError is not emitted for this configuration error.
		return 0, planErr
	}
	if groupInSharedTx {
		if err := f.applyGroupInSharedTx(
			ctx, pending, installedBy, isPreviousVersioned,
		); err != nil {
			return 0, err
		}
		return len(pending), nil
	}
	for _, info := range pending {
		m := info.Resolved
		if m == nil {
			continue
		}
		if *isPreviousVersioned && m.Kind == KindRepeatable {
			if err := f.runCallbacks(ctx, EventAfterVersioned); err != nil {
				return 0, err
			}
			if err := f.runCallbacks(ctx, EventBeforeRepeatables); err != nil {
				return 0, err
			}
			*isPreviousVersioned = false
		}
		if err := f.applyOne(ctx, m, installedBy); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}

// applyOne 执行单个迁移并在历史表中登记。
func (f *Flyway) applyOne(ctx context.Context, m *Migration, installedBy string) error {
	f.log("迁移 %s（%s）...", m.String(), m.Description)
	if f.dialect.SupportsDDLTransactions() &&
		effectiveExecuteInTransaction(
			*f.cfg.ExecuteInTransaction,
			m.ExecuteInTransaction,
			m.NonTransactional,
		) {
		return f.applyInTx(ctx, m, installedBy)
	}
	return f.applyNoTx(ctx, m, installedBy)
}

// applyInTx 在同一事务内完成生命周期 callback、DDL 和历史写入。
// The transaction lives on a dedicated connection so search_path/current
// database changes are guaranteed to apply to every migration statement.
func (f *Flyway) applyInTx(ctx context.Context, m *Migration, installedBy string) error {
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败: %w", err)
	}
	defer conn.Close()
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func() { _ = tx.Rollback() }

	rank, err := f.history.NextRankTx(ctx, tx)
	if err != nil {
		rollback()
		return err
	}
	before, after := migrationLifecycleEvents(m)
	start := time.Now()
	if err := f.runCallbacksWithExecutor(ctx, before, tx); err != nil {
		rollback()
		return err
	}
	execErr := f.executeMigrationStatements(ctx, m, tx, migrationStatementEvents(m))
	if execErr != nil {
		if cbErr := f.runCallbacksWithExecutor(ctx, migrationLifecycleErrorEvent(m), tx); cbErr != nil {
			f.warn("执行迁移错误 callback 失败: %v", cbErr)
		}
		rollback()
		return execErr
	}
	if err := f.runCallbacksWithExecutor(ctx, after, tx); err != nil {
		rollback()
		return err
	}
	elapsed := int(time.Since(start).Milliseconds())
	if err := f.history.AddAppliedTx(ctx, tx, m, rank, elapsed, installedBy); err != nil {
		rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	f.log("  -> 成功，耗时 %d ms", elapsed)
	return nil
}

// applyNoTx executes a migration when the database does not support
// transactional DDL, or when executeInTransaction=false was requested.
//
// Statements intentionally run on the database handle, i.e. in autocommit
// mode. Wrapping this path in a transaction would make PostgreSQL
// CREATE INDEX CONCURRENTLY fail and would roll back prior successful DDL,
// unlike Flyway's non-transactional execution contract.
//
// MySQL needs special handling because the JDBC driver used by Flyway keeps a
// single connection in autocommit=false mode. A DDL statement performs an
// implicit commit, after which subsequent DML (including statement callbacks)
// belongs to a new transaction segment. A later statement error rolls back only
// that tail. database/sql's *sql.Tx cannot represent this transition reliably,
// so use a dedicated *sql.Conn and explicit MySQL transaction statements.
func (f *Flyway) applyNoTx(ctx context.Context, m *Migration, installedBy string) error {
	if f.dialect.Name() == "mysql" {
		return f.applyNoTxMySQL(ctx, m, installedBy)
	}
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败: %w", err)
	}
	defer conn.Close()
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		return err
	}
	rank, err := f.history.nextRank(ctx, conn, TypeSQLMigration)
	if err != nil {
		return err
	}
	before, after := migrationLifecycleEvents(m)
	start := time.Now()
	if err := f.runCallbacksWithExecutor(ctx, before, conn); err != nil {
		return err
	}
	execErr := f.executeMigrationStatements(ctx, m, conn, migrationStatementEvents(m))
	if execErr != nil {
		if cbErr := f.runCallbacksWithExecutor(ctx, migrationLifecycleErrorEvent(m), conn); cbErr != nil {
			f.warn("执行迁移错误 callback 失败: %v", cbErr)
		}
		elapsed := int(time.Since(start).Milliseconds())
		// Flyway only records a failed migration on databases without DDL
		// transactions. PostgreSQL keeps its history untouched when an explicit
		// executeInTransaction=false migration fails; MySQL records the failure.
		if !f.dialect.SupportsDDLTransactions() {
			if herr := f.history.addFailed(ctx, conn, m, rank, elapsed, installedBy); herr != nil {
				f.warn("写入失败记录时出错: %v", herr)
			}
		}
		return execErr
	}
	if err := f.runCallbacksWithExecutor(ctx, after, conn); err != nil {
		return err
	}
	elapsed := int(time.Since(start).Milliseconds())
	if err := f.history.addApplied(ctx, conn, m, rank, elapsed, installedBy); err != nil {
		return err
	}
	f.log("  -> 成功，耗时 %d ms", elapsed)
	return nil
}

func (f *Flyway) applyNoTxMySQL(ctx context.Context, m *Migration, installedBy string) error {
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败: %w", err)
	}
	defer conn.Close()
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		return err
	}
	rank, err := f.history.nextRank(ctx, conn, TypeSQLMigration)
	if err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "SET autocommit=0"); err != nil {
		return err
	}
	restoreAutocommit := func() {
		_, _ = conn.ExecContext(context.Background(), "SET autocommit=1")
	}
	defer restoreAutocommit()
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return err
	}
	before, after := migrationLifecycleEvents(m)
	start := time.Now()
	if err := f.runCallbacksWithExecutor(ctx, before, conn); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	execErr := f.executeMigrationStatements(ctx, m, conn, migrationStatementEvents(m))
	if execErr != nil {
		if cbErr := f.runCallbacksWithExecutor(ctx, migrationLifecycleErrorEvent(m), conn); cbErr != nil {
			f.warn("执行迁移错误 callback 失败: %v", cbErr)
		}
		// The statement and migration-error callbacks run on this connection.
		// Rollback removes their DML as well as any DML after the last implicit
		// DDL commit, matching Flyway's MySQL/JDBC behavior.
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		elapsed := int(time.Since(start).Milliseconds())
		// Flyway only records a failed migration on databases without DDL
		// transactions. PostgreSQL keeps its history untouched when an explicit
		// executeInTransaction=false migration fails; MySQL records the failure.
		if !f.dialect.SupportsDDLTransactions() {
			if herr := f.history.addFailed(ctx, conn, m, rank, elapsed, installedBy); herr != nil {
				f.warn("写入失败记录时出错: %v", herr)
			}
		}
		return execErr
	}
	if err := f.runCallbacksWithExecutor(ctx, after, conn); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	elapsed := int(time.Since(start).Milliseconds())
	if err := f.history.addApplied(ctx, conn, m, rank, elapsed, installedBy); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	f.log("  -> 成功，耗时 %d ms", elapsed)
	return nil
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------------- info

// InfoEntry 对应 Flyway info 输出的一行。
type InfoEntry struct {
	Category       string
	Version        string
	VersionNull    bool
	Description    string
	Type           string
	State          string
	InstalledOn    string // MigrationInfo.getInstalledOn(): database wall clock rendered like java.sql.Timestamp.toString().
	InstalledOnUTC string // CommandResultFactory/InfoOutput: Date.toInstant().toString() in the JVM local zone.
	InstalledBy    string
	Script         string
	Checksum       sql.NullInt64
	InstalledRank  *int

	// PhysicalLocation is non-nil only for resolved migrations that exist on
	// disk. MigrationConfigPrinter relies on this distinction.
	PhysicalLocation string

	// CLI rendering fields. Keep the legacy fields above unchanged for the
	// internal parity harness.
	StateDisplay            string // MigrationState.getDisplayName()
	InstalledOnDisplay      string // DateUtils.formatDateAsIsoString(): yyyy-MM-dd HH:mm:ss
	Undoable                string // ASCII table: Yes / No / empty
	UndoableJSON            string // InfoOutput undoable, without the DELETE/DELETED exception
	UndoFilepath            string // Physical location of the matching filtered undo script
	VersionForID            string // MigrationVersion.getVersion()
	RawVersion              string
	ExecutionTime           int
	ShouldExecuteExpression string
}

// InfoResult 对应 CLI info 的完整结果：Schema version 始终基于过滤前的
// 全部迁移计算，Entries 则是应用 MigrationFilter 之后的行。
type InfoResult struct {
	SchemaVersion          string
	Entries                []InfoEntry
	AllSchemasEmpty        bool
	DatabaseName           string
	DatabaseProductName    string
	DatabaseProductVersion string
	EnvironmentName        string
	SchemaName             string
	HistoryExists          bool
	HistoryTableName       string
}

// Info 生成 Flyway info 的等价视图。
func (f *Flyway) Info(ctx context.Context) ([]InfoEntry, error) {
	result, err := f.InfoResultWithFilter(ctx, nil)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

// InfoWithFilter 生成经过 MigrationFilterImpl 语义过滤的 info 视图。
// AVAILABLE 迁移始终保留，这是 MigrationInfoServiceImpl.all(filter) 的特例。
func (f *Flyway) InfoWithFilter(ctx context.Context, filter *InfoFilter) ([]InfoEntry, error) {
	result, err := f.InfoResultWithFilter(ctx, filter)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

// InfoResultWithFilter 复刻 Main.executeOperation("info") 的两次视图：
// 先用全部迁移计算 current/schema version，再按 filter 生成输出结果。
func (f *Flyway) InfoResultWithFilter(ctx context.Context, filter *InfoFilter) (InfoResult, error) {
	snapshot, err := f.InfoSnapshot(ctx)
	if err != nil {
		return InfoResult{}, err
	}
	return snapshot.Result(filter), nil
}

type undoCandidate struct {
	Version  Version
	Location string
}

func undoCandidates(infos []*MigrationInfo) []undoCandidate {
	candidates := make([]undoCandidate, 0)
	for _, info := range infos {
		if !isUndoType(info.Type()) {
			continue
		}
		version := info.Version()
		if version == nil {
			continue
		}
		candidates = append(candidates, undoCandidate{Version: *version, Location: info.PhysicalLocation()})
	}
	return candidates
}

func infoEntryFor(info *MigrationInfo, undoable []undoCandidate) InfoEntry {
	version := info.Version()
	state := info.State()
	e := InfoEntry{
		Category:                infoCategory(info),
		Version:                 info.VersionText(),
		VersionNull:             version == nil,
		Description:             info.Description(),
		Type:                    info.Type(),
		State:                   string(state),
		StateDisplay:            state.DisplayName(),
		Script:                  info.Script(),
		Checksum:                info.Checksum(),
		InstalledRank:           info.InstalledRank(),
		Undoable:                infoUndoableStatus(info, undoable, true),
		UndoableJSON:            infoUndoableStatus(info, undoable, false),
		UndoFilepath:            infoUndoFilepath(info, undoable),
		RawVersion:              info.RawVersion(),
		ExecutionTime:           info.ExecutionTime(),
		ShouldExecuteExpression: info.ShouldExecuteExpression(),
		PhysicalLocation:        info.PhysicalLocation(),
	}
	if version != nil {
		e.VersionForID = version.GetVersion()
	}
	if info.Applied != nil {
		e.InstalledOn = formatLocalTimestamp(info.Applied.InstalledOn)
		e.InstalledOnUTC = formatInstalledOnUTC(info.Applied.InstalledOn)
		e.InstalledOnDisplay = info.Applied.InstalledOn.Format("2006-01-02 15:04:05")
		e.InstalledBy = info.Applied.InstalledBy
	}
	return e
}

func infoCategory(info *MigrationInfo) string {
	typ := info.Type()
	if isSyntheticType(typ) {
		return ""
	}
	if info.Version() == nil {
		return "Repeatable"
	}
	if isUndoType(typ) {
		return "Undo"
	}
	if isBaselineType(typ) {
		return "Baseline"
	}
	return "Versioned"
}

func infoUndoableStatus(info *MigrationInfo, undoable []undoCandidate, textTable bool) string {
	version := info.Version()
	state := info.State()
	deleteException := textTable && (strings.EqualFold(info.Type(), TypeDelete) || state == StateDeleted)
	if version == nil || deleteException || isUndoType(info.Type()) || state == StateUndone {
		return ""
	}
	if state.IsFailed() {
		return "No"
	}
	for _, candidate := range undoable {
		if version.Compare(candidate.Version) == 0 {
			return "Yes"
		}
	}
	return "No"
}

func infoUndoFilepath(info *MigrationInfo, undoable []undoCandidate) string {
	version := info.Version()
	state := info.State()
	if version == nil || isUndoType(info.Type()) || state == StateUndone || state.IsFailed() {
		return ""
	}
	for _, candidate := range undoable {
		if version.Compare(candidate.Version) == 0 {
			return candidate.Location
		}
	}
	return ""
}

// infoSchemaVersion mirrors MigrationInfoServiceImpl.current():
// highest applied versioned row first, then the latest applied repeatable.
func infoSchemaVersion(infos []*MigrationInfo) string {
	var current *MigrationInfo
	for _, info := range infos {
		state := info.State()
		version := info.Version()
		if !state.IsApplied() || state == StateDeleted || strings.EqualFold(info.Type(), TypeDelete) ||
			state == StateUndone || version == nil {
			continue
		}
		if current == nil || version.Compare(*current.Version()) > 0 {
			current = info
		}
	}
	if current != nil {
		return current.Version().String()
	}
	for i := len(infos) - 1; i >= 0; i-- {
		info := infos[i]
		state := info.State()
		if state.IsApplied() && state != StateDeleted && !strings.EqualFold(info.Type(), TypeDelete) &&
			state != StateUndone && info.Version() == nil {
			return EmptyVersion().String()
		}
	}
	return EmptyVersion().String()
}

// formatLocalTimestamp mirrors java.sql.Timestamp.toString(): the database
// wall-clock fields are displayed unchanged, with fractional seconds trimmed of
// trailing zeroes. Timestamp.toString() always emits at least one fractional
// digit, so an exact second is rendered as ".0".
func formatLocalTimestamp(t time.Time) string {
	base := t.Format("2006-01-02 15:04:05")
	ns := t.Nanosecond()
	if ns == 0 {
		return base + ".0"
	}
	frac := fmt.Sprintf("%09d", ns)
	return base + "." + strings.TrimRight(frac, "0")
}

// timestampAsInstantIn interprets a SQL TIMESTAMP WITHOUT TIME ZONE wall clock
// in the supplied zone, matching java.sql.Timestamp#toInstant() under the JVM
// default time zone. Driver locations are intentionally ignored because both
// PostgreSQL and MySQL return the stored wall-clock fields, not a zoned value.
func timestampAsInstantIn(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// formatInstalledOnUTC mirrors CommandResultFactory.createInfoOutput:
// info.getInstalledOn().toInstant().toString().
func formatInstalledOnUTC(t time.Time) string {
	return formatInstant(timestampAsInstantIn(t, time.Local))
}

func formatInstant(t time.Time) string {
	u := t.UTC()
	base := u.Format("2006-01-02T15:04:05")
	ns := u.Nanosecond()
	if ns == 0 {
		return base + "Z"
	}
	frac := fmt.Sprintf("%09d", ns)
	switch {
	case ns%1_000_000 == 0:
		frac = frac[:3]
	case ns%1_000 == 0:
		frac = frac[:6]
	}
	return base + "." + frac + "Z"
}

// ---------------------------------------------------------------- validate

// validateForCommand builds infos and validates them using official defaults.
func (f *Flyway) validateForCommand(ctx context.Context, schemaCheck bool, extraIgnore ...string) ([]*MigrationInfo, *MigrationInfoContext, error) {
	if err := f.runCallbacks(ctx, EventBeforeValidate); err != nil {
		return nil, nil, err
	}
	infos, context, err := f.validateCore(ctx, schemaCheck, extraIgnore...)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterValidateError)
		return nil, nil, err
	}
	if err := f.runCallbacks(ctx, EventAfterValidate); err != nil {
		return nil, nil, err
	}
	return infos, context, nil
}

// validateCore performs validation itself without callback events.
func (f *Flyway) validateCore(ctx context.Context, schemaCheck bool, extraIgnore ...string) ([]*MigrationInfo, *MigrationInfoContext, error) {
	conn, err := f.commandConn(ctx, "validate 状态读取")
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	return f.validateCoreWithExecutor(ctx, conn, schemaCheck, extraIgnore...)
}

func (f *Flyway) validateCoreWithExecutor(ctx context.Context, exec dbExecutor, schemaCheck bool, extraIgnore ...string) ([]*MigrationInfo, *MigrationInfoContext, error) {
	schemas, err := f.targetSchemas(ctx)
	if err != nil {
		return nil, nil, err
	}
	if schemaCheck {
		var missing []string
		for _, schema := range schemas {
			ok, serr := f.dialect.SchemaExists(ctx, exec, schema)
			if serr != nil {
				return nil, nil, fmt.Errorf("检查 schema %s 是否存在失败: %w", schema, serr)
			}
			if !ok {
				missing = append(missing, schema)
			}
		}
		if len(missing) != 0 && len(f.resolved) != 0 {
			patterns, perr := f.ignorePatterns()
			if perr != nil {
				return nil, nil, perr
			}
			infos, _, berr := BuildMigrationInfos(f.resolved, nil, nil, f.cfg.OutOfOrder, patterns, f.cfg.FailOnMissingTarget, nil)
			if berr != nil {
				return nil, nil, berr
			}
			var errs []error
			for _, info := range infos {
				if info.State() == StatePending {
					errs = append(errs, fmt.Errorf("Unable to validate migrations for schema(s): %s. Schema(s) do not exist", strings.Join(missing, ", ")))
					break
				}
			}
			if len(errs) != 0 {
				return nil, nil, aggregateValidateErrors(errs)
			}
			return infos, nil, nil
		}
	}
	return f.migrateContextWithExecutor(ctx, exec, true, extraIgnore)
}

// ValidateError 官方 validateWithResult 的可返回校验失败。
type ValidateError struct {
	Version     string
	Description string
	Path        string
	Code        string
	Message     string
}

// ValidateResult 对应官方 ValidateResult。
type ValidateResult struct {
	Valid             bool
	InvalidMigrations []ValidateError
	ValidateCount     int
}

func validateResult(infos []*MigrationInfo) ValidateResult {
	var invalid []ValidateError
	for _, info := range infos {
		err := info.Validate()
		if err == nil {
			continue
		}
		path := ""
		if info.Resolved != nil && info.Resolved.Path != "" {
			path = info.Resolved.Path
		}
		e := ValidateError{Description: info.Description(), Message: err.Error(), Code: ErrorCode(err), Path: path}
		if info.Version() != nil {
			e.Version = info.Version().String()
		}
		invalid = append(invalid, e)
	}
	// 官方 DbValidate：存在失败时 validateCount 归零，否则为成功校验的总数。
	count := len(infos)
	if len(invalid) > 0 {
		count = 0
	}
	return ValidateResult{Valid: len(invalid) == 0, InvalidMigrations: invalid, ValidateCount: count}
}

// Validate 执行独立校验，语义与官方 validateWithResult 一致：
// 校验失败作为结果返回，不转化为命令错误。
// ValidateWithResult corresponds to Flyway.validateWithResult(): it returns the
// validation result without turning validation failures into a command error.
func (f *Flyway) ValidateWithResult(ctx context.Context) (ValidateResult, error) {
	if err := f.runCallbacks(ctx, EventBeforeValidate); err != nil {
		return ValidateResult{}, err
	}
	conn, err := f.commandConn(ctx, "validate 状态读取")
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterValidateError)
		return ValidateResult{}, err
	}
	defer conn.Close()
	infos, _, err := f.migrateContextWithExecutor(ctx, conn, false, nil)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterValidateError)
		return ValidateResult{}, err
	}
	result := validateResult(infos)
	if !result.Valid {
		_ = f.runCallbacks(ctx, EventAfterValidateError)
		return result, nil
	}
	if err := f.runCallbacks(ctx, EventAfterValidate); err != nil {
		return ValidateResult{}, err
	}
	return result, nil
}

// Validate corresponds to Flyway.validate(): validation failures become an error.
func (f *Flyway) Validate(ctx context.Context) error {
	result, err := f.ValidateWithResult(ctx)
	if err != nil {
		return err
	}
	if result.Valid {
		return nil
	}
	errs := make([]error, 0, len(result.InvalidMigrations))
	for i := range result.InvalidMigrations {
		errs = append(errs, fmt.Errorf("%s", result.InvalidMigrations[i].Message))
	}
	return aggregateValidateErrors(errs)
}

// ---------------------------------------------------------------- baseline

// Baseline 复刻官方 DbBaseline 流程。
func (f *Flyway) Baseline(ctx context.Context) error {
	lock, err := f.acquireMigrationLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()

	if err := f.runCallbacks(ctx, EventBeforeBaseline); err != nil {
		return err
	}
	if err := f.baselineWithCallbacks(ctx); err != nil {
		_ = f.runCallbacks(ctx, EventAfterBaselineError)
		return err
	}
	return f.runCallbacks(ctx, EventAfterBaseline)
}

func (f *Flyway) baselineWithCallbacks(ctx context.Context) error {
	v, err := ParseVersion(f.cfg.BaselineVersion)
	if err != nil {
		return fmt.Errorf("非法 baselineVersion %q: %w", f.cfg.BaselineVersion, err)
	}
	conn, err := f.commandConn(ctx, "baseline 历史读取/写入")
	if err != nil {
		return err
	}
	defer conn.Close()

	installedBy := f.installedBy(ctx, conn)

	exists, err := f.history.existsWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if !exists {
		return f.history.CreateWithBaselineWithExecutor(ctx, conn, v, f.cfg.BaselineDesc, installedBy)
	}

	marker, err := f.history.GetBaselineMarkerWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if marker != nil {
		mv, _ := ParseVersion(marker.Version)
		if mv.Compare(v) == 0 && marker.Description == f.cfg.BaselineDesc {
			f.log("历史表已按版本 %s 建立基线", v)
			return nil
		}
		return fmt.Errorf("Unable to baseline schema history table %s with (%s, %s) as it has already been baselined with (%s, %s)\nNeed to reset your baseline? Delete the row marked as baseline from the schema history table, set baselineVersion to a higher value, and run baseline again.",
			f.history.Name(), v, f.cfg.BaselineDesc, mv, marker.Description)
	}
	schemasMarker, err := f.history.HasSchemasMarkerWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if schemasMarker && v.Compare(EmptyVersion()) == 0 {
		return fmt.Errorf("Unable to baseline schema history table %s with version 0 as this version was used for schema creation.\nIf you are sure you want to do this, remove the version 0 marker and run baseline again.", f.history.Name())
	}
	nonSynthetic, err := f.history.HasNonSyntheticAppliedMigrationsWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if nonSynthetic {
		return fmt.Errorf("Unable to baseline schema history table %s as it already contains migrations.\nNeed to reset your baseline? Delete the row marked as baseline from the schema history table, set baselineVersion to a higher value, and run baseline again.", f.history.Name())
	}
	applied, err := f.history.AllWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return fmt.Errorf("Unable to baseline schema history table %s as it already exists, and is empty.\nDelete the schema history table, and run baseline again.", f.history.Name())
	}
	return fmt.Errorf("Unable to baseline schema history table %s as it already contains migrations.\nDelete the schema history table, and run baseline again.\nNeed to reset your baseline? Delete the row marked as baseline from the schema history table, set baselineVersion to a higher value, and run baseline again.", f.history.Name())
}

// ---------------------------------------------------------------- repair

// Repair 复刻官方 repair 顺序。
func (f *Flyway) Repair(ctx context.Context) error {
	lock, err := f.acquireMigrationLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()

	if err := f.runCallbacks(ctx, EventBeforeRepair); err != nil {
		return err
	}
	conn, err := f.commandConn(ctx, "repair 历史读取/更新")
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	defer conn.Close()

	exists, err := f.history.existsWithExecutor(ctx, conn)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	if !exists {
		f.log("历史表不存在，无需 repair")
		return f.runCallbacks(ctx, EventAfterRepair)
	}

	removed, rerr := f.history.RemoveFailedWithExecutor(ctx, conn)
	if rerr != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return rerr
	}
	if removed != 0 {
		f.log("已清理 %d 条失败记录", removed)
	}

	patterns, err := ParseIgnorePatterns(f.cfg.IgnoreMigrationPatterns)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	target, err := f.targetVersion()
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	applied, err := f.history.AllWithExecutor(ctx, conn)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	infos, _, err := BuildMigrationInfos(f.resolved, applied, target, f.cfg.OutOfOrder, patterns, f.cfg.FailOnMissingTarget, nil)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	installedBy := f.installedBy(ctx, conn)
	for _, info := range infos {
		state := info.State()
		if state != StateMissingSuccess && state != StateMissingFailed && state != StateFutureSuccess && state != StateFutureFailed {
			continue
		}
		if info.Context.IsMissingIgnored() && (state == StateMissingSuccess || state == StateMissingFailed) {
			continue
		}
		if info.Context.IsFutureIgnored() && (state == StateFutureSuccess || state == StateFutureFailed) {
			continue
		}
		if info.Applied == nil {
			continue
		}
		if err := f.history.AddDeleteWithExecutor(ctx, conn, *info.Applied, installedBy); err != nil {
			_ = f.runCallbacks(ctx, EventAfterRepairError)
			return err
		}
		f.log("已将缺失迁移 %s 标记为删除", info.Description())
	}

	applied, err = f.history.AllWithExecutor(ctx, conn)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	infos, _, err = BuildMigrationInfos(f.resolved, applied, target, f.cfg.OutOfOrder, patterns, f.cfg.FailOnMissingTarget, nil)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterRepairError)
		return err
	}
	for _, info := range infos {
		if info.Resolved == nil || info.Applied == nil || info.Context.IsMissingIgnored() && (info.State() == StateMissingSuccess || info.State() == StateMissingFailed) {
			continue
		}
		if info.Resolved.Kind == KindRepeatable {
			// Official repair updates a repeatable migration only when its applied
			// checksum equals the equivalent (raw-resource) checksum and differs
			// from the placeholder-resolved checksum.
			equivalent := info.Resolved.EquivalentChecksum
			if equivalent == nil || !info.Applied.Checksum.Valid ||
				info.Applied.Checksum.Int64 != *equivalent ||
				info.Applied.Checksum.Int64 == info.Resolved.Checksum {
				continue
			}
		} else {
			if info.State() == StateBelowBaseline || info.State() == StateBaselineIgnored || info.State() == StateSuperseded || info.State() == StateDeleted || info.State() == StateUndone {
				continue
			}
			same := info.Applied.Description == abbreviateDescription(info.Resolved.Description) &&
				info.Applied.Checksum.Valid && info.Applied.Checksum.Int64 == info.Resolved.Checksum &&
				typesEqual(info.Applied.Type, info.Resolved.Type())
			if same {
				continue
			}
		}
		if err := f.history.UpdateAppliedWithExecutor(ctx, conn, *info.Applied, info.Resolved); err != nil {
			_ = f.runCallbacks(ctx, EventAfterRepairError)
			return err
		}
		f.log("已对齐迁移 %s 的历史记录", info.Description())
	}
	return f.runCallbacks(ctx, EventAfterRepair)
}
func checksumMatchesWithoutBeingIdentical(applied sql.NullInt64, resolved int64) bool {
	if !applied.Valid {
		return false
	}
	a := uint32(applied.Int64)
	r := uint32(resolved)
	return a != r && int32(a) == -int32(r)
}

// ---------------------------------------------------------------- clean

// Clean mirrors DbClean/CleanExecutor for PostgreSQL and MySQL. The migration
// lock is acquired before BEFORE_CLEAN, exactly like the other commands.
func (f *Flyway) Clean(ctx context.Context) error {
	if f.cfg.CleanDisabled == nil || *f.cfg.CleanDisabled {
		return fmt.Errorf("Unable to execute clean as it has been disabled with the 'flyway.cleanDisabled' property.")
	}
	lock, err := f.acquireMigrationLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()

	if err := f.runCallbacks(ctx, EventBeforeClean); err != nil {
		return err
	}
	schemas, err := f.targetSchemas(ctx)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterCleanError)
		return err
	}

	conn, err := f.openConn(ctx)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterCleanError)
		return fmt.Errorf("获取 clean 连接失败: %w", err)
	}
	defer conn.Close()

	if err := f.cleanWithConnection(ctx, conn, schemas); err != nil {
		_ = f.runCallbacks(ctx, EventAfterCleanError)
		return err
	}
	if err := f.runCallbacks(ctx, EventAfterClean); err != nil {
		return err
	}
	f.log("clean 完成")
	return nil
}

func (f *Flyway) cleanWithConnection(ctx context.Context, conn *sql.Conn, schemas []string) error {
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		return err
	}

	// A failure to read the marker is only a warning in Flyway. The command
	// continues without dropping schemas.
	dropSchemas, err := f.history.getSchemasCreatedByFlywayWithExecutor(ctx, conn)
	if err != nil {
		f.warn("Error while checking whether the schemas should be dropped. Schemas will not be dropped: %v", err)
		dropSchemas = nil
	}

	// Flyway's Database.cleanPreSchemas/cleanPostSchemas are empty for both
	// PostgreSQL and MySQL, so no database-level pre/post work is required.

	existing := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		ok, err := f.dialect.SchemaExists(ctx, conn, schema)
		if err != nil {
			return fmt.Errorf("检查 schema %s 是否存在失败: %w", schema, err)
		}
		if !ok {
			msg := "Unable to clean unknown schema: " + f.dialect.Quote(schema)
			f.warn("%s", msg)
			continue
		}
		existing = append(existing, schema)
	}

	if err := f.cleanSchemasTwice(ctx, conn, existing, dropSchemas); err != nil {
		return err
	}

	for _, schema := range schemas {
		if !containsString(dropSchemas, schema) {
			continue
		}
		if err := f.dropSchemaWithExecutor(ctx, conn, schema); err != nil {
			f.warn("Unable to drop schema %s. It was cleaned instead.", f.dialect.Quote(schema))
		} else {
			f.log("Successfully dropped schema %s", f.dialect.Quote(schema))
		}
	}
	return nil
}

func (f *Flyway) cleanSchemasTwice(ctx context.Context, conn *sql.Conn, schemas, dropSchemas []string) error {
	for _, schema := range schemas {
		if containsString(dropSchemas, schema) {
			_ = f.runCleanOperation(ctx, conn, func(exec dbExecutor) error {
				return f.dialect.CleanSchema(ctx, exec, schema)
			})
			continue
		}
		if err := f.runCleanOperation(ctx, conn, func(exec dbExecutor) error {
			return f.dialect.CleanSchema(ctx, exec, schema)
		}); err != nil {
			return err
		}
	}
	for i := len(schemas) - 1; i >= 0; i-- {
		schema := schemas[i]
		if containsString(dropSchemas, schema) {
			_ = f.runCleanOperation(ctx, conn, func(exec dbExecutor) error {
				return f.dialect.CleanSchema(ctx, exec, schema)
			})
			continue
		}
		if err := f.runCleanOperation(ctx, conn, func(exec dbExecutor) error {
			return f.dialect.CleanSchema(ctx, exec, schema)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (f *Flyway) runCleanOperation(ctx context.Context, conn *sql.Conn, fn func(dbExecutor) error) error {
	if f.dialect.Name() == "postgresql" && f.cfg.ExecuteInTransaction != nil && *f.cfg.ExecuteInTransaction {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开启 clean 事务失败: %w", err)
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交 clean 事务失败: %w", err)
		}
		return nil
	}
	return fn(conn)
}

func (f *Flyway) dropSchemaWithExecutor(ctx context.Context, conn *sql.Conn, schema string) error {
	return f.runCleanOperation(ctx, conn, func(exec dbExecutor) error {
		_, err := exec.ExecContext(ctx, f.dialect.DropSchemaDDL(schema))
		return err
	})
}

// ---------------------------------------------------------------- undo

// Undo 执行最新一个版本化迁移的撤销脚本。
func (f *Flyway) Undo(ctx context.Context) error {
	lock, err := f.acquireMigrationLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()

	if err := f.runCallbacks(ctx, EventBeforeUndo); err != nil {
		return err
	}
	conn, err := f.commandConn(ctx, "undo 历史读取")
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return err
	}
	defer conn.Close()

	exists, err := f.history.existsWithExecutor(ctx, conn)
	if err != nil {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return err
	}
	if !exists {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return fmt.Errorf("历史表不存在，无可撤销的迁移")
	}
	applied, err := f.history.AllWithExecutor(ctx, conn)
	if err != nil {
		return err
	}
	var last *SchemaHistoryRow
	for i := len(applied) - 1; i >= 0; i-- {
		if applied[i].Success && applied[i].VersionValid && applied[i].Type == TypeSQLMigration {
			last = &applied[i]
			break
		}
	}
	if last == nil {
		return fmt.Errorf("没有可撤销的版本化迁移")
	}
	lastVersion, err := ParseVersion(last.Version)
	if err != nil {
		return fmt.Errorf("历史记录版本非法 %q: %w", last.Version, err)
	}
	var undo *Migration
	for _, m := range f.resolved {
		if m.Kind == KindUndo && m.Version.Compare(lastVersion) == 0 {
			undo = m
			break
		}
	}
	if undo == nil {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return fmt.Errorf("缺少版本 %s 的撤销脚本（期望文件名 U%s__*.sql）", last.Version, last.Version)
	}

	installedBy := f.installedBy(ctx, conn)
	if err := f.applyOne(ctx, undo, installedBy); err != nil {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return err
	}
	if err := f.history.AddDeleteWithExecutor(ctx, conn, *last, installedBy); err != nil {
		_ = f.runCallbacks(ctx, EventAfterUndoError)
		return err
	}
	f.log("已撤销版本 %s", last.Version)
	return f.runCallbacks(ctx, EventAfterUndo)
}

// ---------------------------------------------------------------- helpers

// ResolvedMigrations 返回扫描结果，供外部展示。
func (f *Flyway) ResolvedMigrations() []*Migration { return f.resolved }

func baselineVersionFromApplied(applied []SchemaHistoryRow) *Version {
	var baseline *Version
	for _, row := range applied {
		if !row.Success || !isBaselineType(row.Type) || !row.VersionValid {
			continue
		}
		v, err := ParseVersion(row.Version)
		if err == nil {
			vv := v
			baseline = &vv
		}
	}
	return baseline
}
