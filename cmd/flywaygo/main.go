package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/linsu/flywaygo/core"
)

const usage = `flywaygo —— Flyway 的 Go 实现（支持 PostgreSQL / MySQL）

用法:
  flywaygo <command> [options]

命令:
  migrate    应用所有待执行的迁移
  info       显示迁移状态
  validate   校验已应用迁移与本地脚本是否一致
  baseline   为已存在的数据库建立基线
  repair     清理失败记录并修正校验和
  clean      清空数据库对象（危险）

配置优先级:
  默认 flyway.conf < FLYWAY_CONFIG_FILES < -configFiles < FLYWAY_* 环境变量 < 显式命令行参数

选项:
  -url        数据库连接串（也可由 flyway.url / FLYWAY_URL 提供）
  -user       数据库用户名（覆盖 URL 中的用户名）
  -password   数据库密码（覆盖 URL 中的密码）
  -driver     驱动: postgres | mysql（可由 -url 自动推断）
  -workingDirectory   工作目录；用于默认配置和相对配置文件路径
  -configFiles        额外配置文件，逗号分隔
  -configFileEncoding 配置文件编码
  -locations  迁移脚本目录，多个用逗号分隔（默认 db/migration）
  -callback-locations  SQL callback 目录，多个用逗号分隔
  -table      历史表名（默认 flyway_schema_history）
  -schemas    目标 schema 列表，多个用逗号分隔
  -default-schema 默认 schema
  -baseline-version   基线版本（默认 1）
  -baseline-on-migrate 库非空且无历史表时自动 baseline
  -out-of-order        允许执行低于当前最高版本的脚本
  -target     迁移到指定版本为止（默认 latest；尾部 ? 表示可选）
  -clean-disabled      禁用 clean 命令（默认 true）

示例:
  flywaygo migrate -url "postgres://u:p@localhost:5432/app?sslmode=disable" -locations ./migrations
  flywaygo info -url "mysql://root:pass@tcp(127.0.0.1:3306)/app?parseTime=true"
`

type stdLogger struct {
	out io.Writer
}

func (l stdLogger) Info(format string, args ...interface{}) {
	fmt.Fprintf(l.out, format+"\n", args...)
}

func (l stdLogger) Warn(format string, args ...interface{}) {
	fmt.Fprintf(l.out, "[WARN] "+format+"\n", args...)
}

type cliDependencies struct {
	loadDefaultConfig     func(installationDirectory, workingDirectory, encoding string) (map[string]string, error)
	loadConfigFile        func(path, encoding string, failIfMissing bool) (map[string]string, error)
	loadDefaultTOML       func(installationDirectory, workingDirectory string) (core.ConfigurationModel, error)
	loadTOMLFiles         func(paths []string) (core.ConfigurationModel, error)
	installationDirectory func() string
	environment           func() map[string]string
	rawEnvironment        func() map[string]string
	fileExists            func(path string) bool
	warn                  func(format string, args ...interface{})
	openDB                func(driver, dsn string) (*sql.DB, error)
}

func defaultCLIDependencies() cliDependencies {
	return cliDependencies{
		loadDefaultConfig:     core.LoadDefaultConfigurationFiles,
		loadConfigFile:        core.LoadConfigurationFile,
		loadDefaultTOML:       core.LoadDefaultTOMLConfigurationFiles,
		loadTOMLFiles:         core.LoadTOMLConfigurationFiles,
		installationDirectory: defaultInstallationDirectory,
		environment:           core.EnvironmentConfiguration,
		rawEnvironment:        core.RawEnvironmentVariables,
		fileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		openDB: openDB,
	}
}

type cliInvocation struct {
	Command string
	Config  core.Config
	Driver  string
	DSN     string
}

func main() {
	// The parity harness intentionally uses the older machine-readable protocol
	// with space-separated flags. Keep it out of the user-facing CLI parser.
	if os.Getenv("FLYWAYGO_INTERNAL_HARNESS") == "1" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(runUserCLI(os.Args[1:], os.Stdout, os.Stderr, defaultCLIDependencies()))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, defaultCLIDependencies())
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps cliDependencies) int {
	invocation, exitCode, stop := resolveInvocation(args, stdout, stderr, deps)
	if stop {
		return exitCode
	}

	db, err := deps.openDB(invocation.Driver, invocation.DSN)
	if err != nil {
		writeError(stderr, err)
		return 1
	}
	defer db.Close()

	invocation.Config.Database = db
	ctx := context.Background()
	fw, err := core.New(ctx, invocation.Config, stdLogger{out: stdout})
	if err != nil {
		writeError(stderr, err)
		return 1
	}

	switch invocation.Command {
	case "migrate":
		err = fw.Migrate(ctx)
		if err == nil {
			fmt.Fprintln(stdout, "RESULT\tmigrate\tsuccess=true")
		}
	case "info":
		var entries []core.InfoEntry
		entries, err = fw.Info(ctx)
		if err == nil {
			fmt.Fprintf(stdout, "RESULT\tinfo\tsuccess=true\tcount=%d\n", len(entries))
			printInfo(entries, stdout)
		}
	case "validate":
		var vr core.ValidateResult
		vr, err = fw.ValidateWithResult(ctx)
		if err == nil {
			fmt.Fprintf(stdout, "RESULT\tvalidate\tvalid=%v\tinvalidMigrations=%d\tvalidateCount=%d\n", vr.Valid, len(vr.InvalidMigrations), vr.ValidateCount)
			for _, invalid := range vr.InvalidMigrations {
				fmt.Fprintf(stdout, "INVALID\t%s\t%s\t%s\t%s\t%s\n", sanitize(invalid.Version), sanitize(invalid.Description), sanitize(invalid.Path), sanitize(invalid.Code), sanitize(invalid.Message))
			}
			return validateExitCode(vr)
		}
	case "baseline":
		err = fw.Baseline(ctx)
		if err == nil {
			baselineResultVersion, parseErr := core.ParseVersion(invocation.Config.BaselineVersion)
			if parseErr != nil {
				err = parseErr
				break
			}
			fmt.Fprintf(stdout, "RESULT\tbaseline\tsuccess=true\tbaselineVersion=%s\n", sanitize(baselineResultVersion.String()))
		}
	case "repair":
		err = fw.Repair(ctx)
		if err == nil {
			fmt.Fprintln(stdout, "RESULT\trepair\tsuccess=true")
		}
	case "clean":
		err = fw.Clean(ctx)
		if err == nil {
			fmt.Fprintln(stdout, "RESULT\tclean\tsuccess=true")
		}
	}

	if err != nil {
		writeError(stderr, err)
		return 1
	}
	return 0
}

type cliFlags struct {
	url                     string
	user                    string
	password                string
	driver                  string
	workingDirectory        string
	configFiles             string
	configFileEncoding      string
	locations               string
	callbackLocations       string
	table                   string
	schemas                 string
	defaultSchema           string
	baselineVersion         string
	baselineDesc            string
	baselineMigrationPrefix string
	baselineOnMigrate       bool
	outOfOrder              bool
	group                   bool
	createSchemas           bool
	ignorePatterns          string
	ignoreMissing           bool
	ignorePending           bool
	target                  string
	cleanDisabled           bool
	pgTxLock                bool
	validateOnMigrate       bool
	validateMigrationNaming bool
	executeInTransaction    bool
	placeholderReplacement  bool
	mixed                   bool
	placeholders            string
	placeholderPrefix       string
	placeholderSuffix       string
	connectRetries          int
	connectRetriesInterval  int
	initSQL                 string
	encoding                string
	detectEncoding          bool
	failOnMissingLocations  bool
	lockRetryCount          int
	skipDefaultCallbacks    bool
	placeholderSeparator    string
	sqlMigrationPrefix      string
	repeatablePrefix        string
	sqlMigrationSeparator   string
	sqlMigrationSuffixes    string
}

func resolveInvocation(args []string, stdout, stderr io.Writer, deps cliDependencies) (cliInvocation, int, bool) {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return cliInvocation{}, 2, true
	}
	cmd := args[0]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Fprint(stdout, usage)
		return cliInvocation{}, 0, true
	}
	if !isCommand(cmd) {
		fmt.Fprintf(stderr, "未知命令: %s\n\n", cmd)
		fmt.Fprint(stderr, usage)
		return cliInvocation{}, 2, true
	}
	if hasHelpFlag(args[1:]) {
		fmt.Fprint(stdout, usage)
		return cliInvocation{}, 0, true
	}

	prepared, err := prepareCLIConfigurationArgs(args[1:])
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stdout, usage) }
	values := bindCLIFlags(fs)
	if err := fs.Parse(prepared.flagArgs); err != nil {
		return cliInvocation{}, 2, true
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "错误: 未知参数 %s\n\n", sanitize(strings.Join(fs.Args(), " ")))
		fmt.Fprint(stderr, usage)
		return cliInvocation{}, 2, true
	}

	workingDirectory := normalizedWorkingDirectory(values.workingDirectory)
	selection, err := selectConfigurationMode(deps, values.configFiles, workingDirectory, stderr)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	if selection.mode == core.ConfigModeModern {
		return resolveModernInvocation(cmd, fs, values, prepared, selection, stdout, stderr, deps)
	}
	return resolveClassicInvocation(cmd, fs, values, prepared, selection, stdout, stderr, deps)
}
func bindCLIFlags(fs *flag.FlagSet) *cliFlags {
	v := &cliFlags{}
	fs.StringVar(&v.url, "url", "", "数据库连接串")
	fs.StringVar(&v.user, "user", "", "数据库用户名")
	fs.StringVar(&v.password, "password", "", "数据库密码")
	fs.StringVar(&v.driver, "driver", "", "驱动: postgres | mysql")
	fs.StringVar(&v.workingDirectory, "working-directory", "", "工作目录")
	fs.StringVar(&v.workingDirectory, "workingDirectory", "", "工作目录")
	fs.StringVar(&v.configFiles, "configFiles", "", "额外配置文件，逗号分隔")
	fs.StringVar(&v.configFileEncoding, "configFileEncoding", "", "配置文件编码")
	fs.StringVar(&v.locations, "locations", "db/migration", "迁移脚本目录，逗号分隔")
	fs.StringVar(&v.callbackLocations, "callback-locations", "", "SQL callback 目录，逗号分隔")
	fs.StringVar(&v.table, "table", "flyway_schema_history", "历史表名")
	fs.StringVar(&v.schemas, "schemas", "", "目标 schema 列表，逗号分隔")
	fs.StringVar(&v.defaultSchema, "default-schema", "", "默认 schema")
	fs.StringVar(&v.baselineVersion, "baseline-version", "1", "基线版本")
	fs.StringVar(&v.baselineDesc, "baseline-description", "<< Flyway Baseline >>", "基线描述")
	fs.StringVar(&v.baselineMigrationPrefix, "baseline-migration-prefix", "B", "磁盘基线脚本前缀")
	fs.BoolVar(&v.baselineOnMigrate, "baseline-on-migrate", false, "库非空时自动 baseline")
	fs.BoolVar(&v.outOfOrder, "out-of-order", false, "允许执行低版本脚本")
	fs.BoolVar(&v.group, "group", false, "同一锁内执行一批迁移")
	fs.BoolVar(&v.createSchemas, "create-schemas", true, "自动创建缺失 schema")
	fs.StringVar(&v.ignorePatterns, "ignore-migration-patterns", "", "忽略规则，逗号分隔")
	fs.BoolVar(&v.ignoreMissing, "ignore-missing", false, "忽略本地缺失的已应用迁移")
	fs.BoolVar(&v.ignorePending, "ignore-pending", false, "忽略未应用的本地迁移")
	fs.StringVar(&v.target, "target", "latest", "迁移目标版本")
	fs.BoolVar(&v.cleanDisabled, "clean-disabled", true, "禁用 clean")
	fs.BoolVar(&v.pgTxLock, "postgresql.transactional.lock", true, "PostgreSQL advisory lock in transaction")
	fs.BoolVar(&v.validateOnMigrate, "validate-on-migrate", true, "迁移前校验")
	fs.BoolVar(&v.validateMigrationNaming, "validate-migration-naming", false, "校验 SQL 迁移文件命名")
	fs.BoolVar(&v.executeInTransaction, "execute-in-transaction", true, "默认在事务中执行脚本")
	fs.BoolVar(&v.placeholderReplacement, "placeholder-replacement", true, "启用 placeholder 替换")
	fs.BoolVar(&v.mixed, "mixed", false, "允许同一脚本混用事务性与非事务性语句")
	fs.StringVar(&v.placeholders, "placeholders", "", "placeholder 参数，格式 k=v，逗号分隔")
	fs.StringVar(&v.placeholderPrefix, "placeholder-prefix", "${", "placeholder 前缀")
	fs.StringVar(&v.placeholderSuffix, "placeholder-suffix", "}", "placeholder 后缀")
	fs.StringVar(&v.placeholderSeparator, "placeholder-separator", ":", "placeholder 分隔符")
	fs.IntVar(&v.connectRetries, "connect-retries", 0, "连接重试次数")
	fs.IntVar(&v.connectRetriesInterval, "connect-retries-interval", 120, "连接重试最大间隔秒数")
	fs.StringVar(&v.initSQL, "init-sql", "", "每次新连接执行的 SQL")
	fs.StringVar(&v.encoding, "encoding", "UTF-8", "迁移文件编码")
	fs.BoolVar(&v.detectEncoding, "detect-encoding", false, "自动检测迁移文件编码")
	fs.BoolVar(&v.failOnMissingLocations, "fail-on-missing-locations", false, "迁移目录不存在时报错")
	fs.IntVar(&v.lockRetryCount, "lock-retry-count", 50, "迁移锁重试次数")
	fs.BoolVar(&v.skipDefaultCallbacks, "skip-default-callbacks", false, "跳过默认 callback")
	fs.StringVar(&v.sqlMigrationPrefix, "sql-migration-prefix", "V", "版本化迁移前缀")
	fs.StringVar(&v.repeatablePrefix, "repeatable-sql-migration-prefix", "R", "可重复迁移前缀")
	fs.StringVar(&v.sqlMigrationSeparator, "sql-migration-separator", "__", "迁移文件名分隔符")
	fs.StringVar(&v.sqlMigrationSuffixes, "sql-migration-suffixes", ".sql", "迁移文件后缀，逗号分隔")
	return v
}

func configFilePaths(raw, workingDirectory string) []string {
	paths := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || strings.HasSuffix(item, ".toml") {
			continue
		}
		if workingDirectory != "" && !filepath.IsAbs(item) {
			item = filepath.Join(workingDirectory, item)
		}
		paths = append(paths, filepath.Clean(item))
	}
	return paths
}

func applyConfigurationProperties(cfg *core.Config, props map[string]string) error {
	filtered := make(map[string]string, len(props))
	for key, value := range props {
		if key == "flyway.configFiles" || key == "flyway.configFileEncoding" {
			continue
		}
		filtered[key] = value
	}
	return core.ApplyConfiguration(cfg, filtered)
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func explicitFlagProperties(fs *flag.FlagSet) map[string]string {
	props := make(map[string]string)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "placeholders" {
			for key, value := range parsePlaceholderAssignments(f.Value.String()) {
				props["flyway.placeholders."+key] = value
			}
			return
		}
		key := flagPropertyName(f.Name)
		if key != "" {
			props[key] = f.Value.String()
		}
	})
	return props
}

// parsePlaceholderAssignments mirrors the CLI's key=value,key2=value2 form.
// Only the first '=' separates a key from its value, so values may contain '='.
func parsePlaceholderAssignments(raw string) map[string]string {
	out := make(map[string]string)
	for _, assignment := range strings.Split(raw, ",") {
		assignment = strings.TrimSpace(assignment)
		if assignment == "" {
			continue
		}
		i := strings.IndexByte(assignment, '=')
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(assignment[:i])
		if key == "" {
			continue
		}
		out[key] = assignment[i+1:]
	}
	return out
}

func flagPropertyName(name string) string {
	switch name {
	case "url":
		return "flyway.url"
	case "user":
		return "flyway.user"
	case "password":
		return "flyway.password"
	case "driver":
		return "flyway.driver"
	case "configFiles", "configFileEncoding", "working-directory", "workingDirectory":
		return ""
	case "locations":
		return "flyway.locations"
	case "callback-locations":
		return "flyway.callbackLocations"
	case "table":
		return "flyway.table"
	case "schemas":
		return "flyway.schemas"
	case "default-schema":
		return "flyway.defaultSchema"
	case "baseline-version":
		return "flyway.baselineVersion"
	case "baseline-description":
		return "flyway.baselineDescription"
	case "baseline-migration-prefix":
		return "flyway.baselineMigrationPrefix"
	case "baseline-on-migrate":
		return "flyway.baselineOnMigrate"
	case "out-of-order":
		return "flyway.outOfOrder"
	case "group":
		return "flyway.group"
	case "create-schemas":
		return "flyway.createSchemas"
	case "ignore-migration-patterns":
		return "flyway.ignoreMigrationPatterns"

	case "target":
		return "flyway.target"
	case "clean-disabled":
		return "flyway.cleanDisabled"
	case "postgresql.transactional.lock":
		return "flyway.postgresql.transactional.lock"
	case "validate-on-migrate":
		return "flyway.validateOnMigrate"
	case "validate-migration-naming":
		return "flyway.validateMigrationNaming"
	case "execute-in-transaction":
		return "flyway.executeInTransaction"
	case "placeholder-replacement":
		return "flyway.placeholderReplacement"
	case "mixed":
		return "flyway.mixed"
	case "placeholders":
		return ""
	case "placeholder-prefix":
		return "flyway.placeholderPrefix"
	case "placeholder-suffix":
		return "flyway.placeholderSuffix"
	case "placeholder-separator":
		return "flyway.placeholderSeparator"
	case "connect-retries":
		return "flyway.connectRetries"
	case "connect-retries-interval":
		return "flyway.connectRetriesInterval"
	case "init-sql":
		return "flyway.initSql"
	case "encoding":
		return "flyway.encoding"
	case "detect-encoding":
		return "flyway.detectEncoding"
	case "fail-on-missing-locations":
		return "flyway.failOnMissingLocations"
	case "lock-retry-count":
		return "flyway.lockRetryCount"
	case "skip-default-callbacks":
		return "flyway.skipDefaultCallbacks"
	case "sql-migration-prefix":
		return "flyway.sqlMigrationPrefix"
	case "repeatable-sql-migration-prefix":
		return "flyway.repeatableSqlMigrationPrefix"
	case "sql-migration-separator":
		return "flyway.sqlMigrationSeparator"
	case "sql-migration-suffixes":
		return "flyway.sqlMigrationSuffixes"
	default:
		return ""
	}
}

func isCommand(cmd string) bool {
	switch cmd {
	case "migrate", "info", "validate", "baseline", "repair", "clean":
		return true
	default:
		return false
	}
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func writeError(w io.Writer, err error) {
	fmt.Fprintf(w, "ERROR\t%s\n", sanitize(err.Error()))
}

func validateExitCode(vr core.ValidateResult) int {
	if !vr.Valid {
		return 1
	}
	return 0
}

func sanitize(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

// parseURL 同时支持 JDBC 风格（jdbc:postgresql://）与简短 URL（postgres:// / mysql://）。
func parseURL(rawURL, driverHint string) (string, string, error) {
	return parseURLWithCredentials(rawURL, driverHint, "", "")
}

func parseURLWithCredentials(rawURL, driverHint, user, password string) (string, string, error) {
	u, err := applyURLCredentials(rawURL, driverHint, user, password)
	if err != nil {
		return "", "", err
	}
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(u, "jdbc:")

	var drv string
	switch {
	case strings.HasPrefix(u, "postgres://"), strings.HasPrefix(u, "postgresql://"):
		drv = "postgres"
		u = strings.Replace(u, "postgresql://", "postgres://", 1)
		// lib/pq 接受 postgres:// 形式的 URL
		return drv, u, nil
	case strings.HasPrefix(u, "mysql://"):
		drv = "mysql"
		// 转为 go-sql-driver 的 DSN 形式: user:pass@tcp(host:port)/db?params
		rest := strings.TrimPrefix(u, "mysql://")

		// 先剥离库名与参数，避免其中的 "@" ":" 干扰解析
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", "", fmt.Errorf("mysql 连接串缺少数据库名: %s", rawURL)
		}
		hostAndUser := rest[:slash]
		dbAndParams := rest[slash+1:]

		userInfo := ""
		host := hostAndUser
		if at := strings.LastIndex(hostAndUser, "@"); at >= 0 {
			userInfo = hostAndUser[:at]
			host = hostAndUser[at+1:]
		}
		if strings.TrimSpace(userInfo) == "" {
			return "", "", fmt.Errorf("mysql 连接串缺少用户名: %s", rawURL)
		}

		// 支持 host:port 与 tcp(host:port) 两种写法
		if strings.HasPrefix(host, "tcp(") && strings.HasSuffix(host, ")") {
			host = host[4 : len(host)-1]
		}
		if !strings.Contains(host, ":") {
			host += ":3306"
		}
		return drv, fmt.Sprintf("%s@tcp(%s)/%s", userInfo, host, dbAndParams), nil
	}

	if driverHint != "" {
		return strings.ToLower(driverHint), u, nil
	}
	return "", "", fmt.Errorf("无法从 %q 推断驱动，请用 -driver 指定 postgres 或 mysql", rawURL)
}

func applyURLCredentials(rawURL, driverHint, user, password string) (string, error) {
	if user == "" && password == "" {
		return rawURL, nil
	}
	trimmed := strings.TrimSpace(rawURL)
	trimmed = strings.TrimPrefix(trimmed, "jdbc:")
	switch {
	case strings.HasPrefix(trimmed, "postgres://"), strings.HasPrefix(trimmed, "postgresql://"):
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("解析连接串失败: %w", err)
		}
		existingUser, existingPassword := "", ""
		if parsed.User != nil {
			existingUser = parsed.User.Username()
			existingPassword, _ = parsed.User.Password()
		}
		if user == "" {
			user = existingUser
		}
		if password == "" {
			password = existingPassword
		}
		if user == "" {
			return "", fmt.Errorf("指定 password 时必须同时指定 user 或 URL 中已有用户名")
		}
		if password == "" {
			parsed.User = url.User(user)
		} else {
			parsed.User = url.UserPassword(user, password)
		}
		return parsed.String(), nil
	case strings.HasPrefix(trimmed, "mysql://"):
		return applyMySQLURLCredentials(trimmed, user, password)
	}

	hint := strings.ToLower(driverHint)
	if hint == "mysql" {
		if at := strings.LastIndex(trimmed, "@tcp("); at >= 0 {
			userInfo := trimmed[:at]
			existingUser, existingPassword := splitUserInfo(userInfo)
			if user == "" {
				user = existingUser
			}
			if password == "" {
				password = existingPassword
			}
			if user == "" {
				return "", fmt.Errorf("指定 password 时必须同时指定 user 或 DSN 中已有用户名")
			}
			return joinUserInfo(user, password) + trimmed[at:], nil
		}
	}
	return trimmed, nil
}

func applyMySQLURLCredentials(rawURL, user, password string) (string, error) {
	rest := strings.TrimPrefix(rawURL, "mysql://")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", fmt.Errorf("mysql 连接串缺少数据库名: %s", rawURL)
	}
	hostAndUser := rest[:slash]
	dbAndParams := rest[slash+1:]
	userInfo := ""
	host := hostAndUser
	if at := strings.LastIndex(hostAndUser, "@"); at >= 0 {
		userInfo = hostAndUser[:at]
		host = hostAndUser[at+1:]
	}
	existingUser, existingPassword := splitUserInfo(userInfo)
	if user == "" {
		user = existingUser
	}
	if password == "" {
		password = existingPassword
	}
	if user == "" {
		return "", fmt.Errorf("指定 password 时必须同时指定 user 或 MySQL URL 中已有用户名")
	}
	if host == "" {
		return "", fmt.Errorf("mysql 连接串缺少主机名: %s", rawURL)
	}
	return "mysql://" + joinUserInfo(user, password) + "@" + host + "/" + dbAndParams, nil
}

func splitUserInfo(userInfo string) (string, string) {
	if i := strings.IndexByte(userInfo, ':'); i >= 0 {
		return userInfo[:i], userInfo[i+1:]
	}
	return userInfo, ""
}

func joinUserInfo(user, password string) string {
	if password == "" {
		return user
	}
	return user + ":" + password
}

func printInfo(entries []core.InfoEntry, out io.Writer) {
	for _, e := range entries {
		version := e.Version
		if e.VersionNull || version == "" {
			version = "NULL"
		}
		category := e.Category
		checksum := "NULL"
		if e.Checksum.Valid {
			checksum = strconv.FormatInt(e.Checksum.Int64, 10)
		}
		rank := "NULL"
		if e.InstalledRank != nil {
			rank = strconv.Itoa(*e.InstalledRank)
		}
		installedBy := e.InstalledBy
		if installedBy == "" {
			installedBy = "NULL"
		}
		installedOn := e.InstalledOn
		if installedOn == "" {
			installedOn = "NULL"
		}
		installedOnUTC := e.InstalledOnUTC
		if installedOnUTC == "" {
			installedOnUTC = "NULL"
		}
		fmt.Fprintf(out, "INFO\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			category, version, sanitize(e.Description), sanitize(e.Type), sanitize(e.State),
			sanitize(e.Script), checksum, rank, sanitize(installedBy),
			sanitize(installedOn), sanitize(installedOnUTC))
	}
}
