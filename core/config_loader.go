package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvironmentConfiguration maps FLYWAY_* environment variables using the same
// explicit whitelist as ConfigUtils.convertKey.
func EnvironmentConfiguration() map[string]string {
	return EnvironmentConfigurationFrom(environmentMap(os.Environ()))
}

// EnvironmentConfigurationFrom is the testable form of EnvironmentConfiguration.
func EnvironmentConfigurationFrom(env map[string]string) map[string]string {
	out := make(map[string]string)
	for key, value := range env {
		if converted := convertEnvironmentKey(key); converted != "" {
			out[converted] = value
		}
	}
	return out
}

func environmentMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		i := strings.IndexByte(entry, '=')
		if i < 0 {
			continue
		}
		out[entry[:i]] = entry[i+1:]
	}
	return out
}

func convertEnvironmentKey(key string) string {
	fixed := map[string]string{
		"FLYWAY_BASELINE_DESCRIPTION":            "flyway.baselineDescription",
		"FLYWAY_BASELINE_MIGRATION_PREFIX":       "flyway.baselineMigrationPrefix",
		"FLYWAY_BASELINE_ON_MIGRATE":             "flyway.baselineOnMigrate",
		"FLYWAY_BASELINE_VERSION":                "flyway.baselineVersion",
		"FLYWAY_BATCH":                           "flyway.batch",
		"FLYWAY_CALLBACKS":                       "flyway.callbacks",
		"FLYWAY_CLEAN_DISABLED":                  "flyway.cleanDisabled",
		"FLYWAY_COMMUNITY_DB_SUPPORT_DISABLED":   "flyway.communityDBSupportEnabled",
		"FLYWAY_CONFIG_FILE_ENCODING":            "flyway.configFileEncoding",
		"FLYWAY_CONFIG_FILES":                    "flyway.configFiles",
		"FLYWAY_CONNECT_RETRIES":                 "flyway.connectRetries",
		"FLYWAY_CONNECT_RETRIES_INTERVAL":        "flyway.connectRetriesInterval",
		"FLYWAY_DEFAULT_SCHEMA":                  "flyway.defaultSchema",
		"FLYWAY_DRIVER":                          "flyway.driver",
		"FLYWAY_DRYRUN_OUTPUT":                   "flyway.dryRunOutput",
		"FLYWAY_ENCODING":                        "flyway.encoding",
		"FLYWAY_EXECUTE_IN_TRANSACTION":          "flyway.executeInTransaction",
		"FLYWAY_DETECT_ENCODING":                 "flyway.detectEncoding",
		"FLYWAY_ERROR_OVERRIDES":                 "flyway.errorOverrides",
		"FLYWAY_GROUP":                           "flyway.group",
		"FLYWAY_IGNORE_MIGRATION_PATTERNS":       "flyway.ignoreMigrationPatterns",
		"FLYWAY_INIT_SQL":                        "flyway.initSql",
		"FLYWAY_INSTALLED_BY":                    "flyway.installedBy",
		"FLYWAY_LICENSE_KEY":                     "flyway.licenseKey",
		"FLYWAY_LOCATIONS":                       "flyway.locations",
		"FLYWAY_CALLBACK_LOCATIONS":              "flyway.callbackLocations",
		"FLYWAY_MIXED":                           "flyway.mixed",
		"FLYWAY_OUT_OF_ORDER":                    "flyway.outOfOrder",
		"FLYWAY_SKIP_EXECUTING_MIGRATIONS":       "flyway.skipExecutingMigrations",
		"FLYWAY_OUTPUT_QUERY_RESULTS":            "flyway.outputQueryResults",
		"FLYWAY_PASSWORD":                        "flyway.password",
		"FLYWAY_LOCK_RETRY_COUNT":                "flyway.lockRetryCount",
		"FLYWAY_PLACEHOLDER_PREFIX":              "flyway.placeholderPrefix",
		"FLYWAY_PLACEHOLDER_REPLACEMENT":         "flyway.placeholderReplacement",
		"FLYWAY_PLACEHOLDER_SUFFIX":              "flyway.placeholderSuffix",
		"FLYWAY_PLACEHOLDER_SEPARATOR":           "flyway.placeholderSeparator",
		"FLYWAY_SCRIPT_PLACEHOLDER_PREFIX":       "flyway.scriptPlaceholderPrefix",
		"FLYWAY_SCRIPT_PLACEHOLDER_SUFFIX":       "flyway.scriptPlaceholderSuffix",
		"FLYWAY_POWERSHELL_EXECUTABLE":           "flyway.powershellExecutable",
		"FLYWAY_REPEATABLE_SQL_MIGRATION_PREFIX": "flyway.repeatableSqlMigrationPrefix",
		"FLYWAY_RESOLVERS":                       "flyway.resolvers",
		"FLYWAY_SCHEMAS":                         "flyway.schemas",
		"FLYWAY_SKIP_DEFAULT_CALLBACKS":          "flyway.skipDefaultCallbacks",
		"FLYWAY_SKIP_DEFAULT_RESOLVERS":          "flyway.skipDefaultResolvers",
		"FLYWAY_SQL_MIGRATION_PREFIX":            "flyway.sqlMigrationPrefix",
		"FLYWAY_SQL_MIGRATION_SEPARATOR":         "flyway.sqlMigrationSeparator",
		"FLYWAY_SQL_MIGRATION_SUFFIXES":          "flyway.sqlMigrationSuffixes",
		"FLYWAY_STREAM":                          "flyway.stream",
		"FLYWAY_TABLE":                           "flyway.table",
		"FLYWAY_TABLESPACE":                      "flyway.tablespace",
		"FLYWAY_TARGET":                          "flyway.target",
		"FLYWAY_LOGGERS":                         "flyway.loggers",
		"FLYWAY_UNDO_SQL_MIGRATION_PREFIX":       "flyway.undoSqlMigrationPrefix",
		"FLYWAY_URL":                             "flyway.url",
		"FLYWAY_USER":                            "flyway.user",
		"FLYWAY_VALIDATE_ON_MIGRATE":             "flyway.validateOnMigrate",
		"FLYWAY_VALIDATE_MIGRATION_NAMING":       "flyway.validateMigrationNaming",
		"FLYWAY_CREATE_SCHEMAS":                  "flyway.createSchemas",
		"FLYWAY_FAIL_ON_MISSING_LOCATIONS":       "flyway.failOnMissingLocations",
		"FLYWAY_KERBEROS_CONFIG_FILE":            "flyway.kerberosConfigFile",
		"FLYWAY_REPORT_FILENAME":                 "flyway.reportFilename",
		"FLYWAY_JAR_DIRS":                        "flyway.jarDirs",
		"FLYWAY_CONFIGURATIONS":                  "flyway.configurations",
	}
	if value, ok := fixed[key]; ok {
		return value
	}
	if strings.HasPrefix(key, "FLYWAY_PLACEHOLDERS_") && len(key) > len("FLYWAY_PLACEHOLDERS_") {
		return "flyway.placeholders." + strings.ToLower(key[len("FLYWAY_PLACEHOLDERS_"):])
	}
	if strings.HasPrefix(key, "FLYWAY_JDBC_PROPERTIES_") && len(key) > len("FLYWAY_JDBC_PROPERTIES_") {
		suffix := key[len("FLYWAY_JDBC_PROPERTIES_"):]
		if suffix == "ACCESSTOKEN" {
			suffix = "accessToken"
		} else {
			suffix = strings.ToLower(suffix)
		}
		return "flyway.jdbcProperties." + suffix
	}
	return ""
}

// LoadConfigurationFile mirrors ConfigUtils.loadConfigurationFile. A missing
// file is empty unless failIfMissing is true. "-" reads stdin as UTF-8.
func LoadConfigurationFile(path, encoding string, failIfMissing bool) (map[string]string, error) {
	if path == "-" {
		return LoadConfigurationFromReader(os.Stdin, "UTF-8")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		if !failIfMissing {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("Unable to load config file: %s", abs)
	}
	file, err := os.Open(path)
	if err != nil {
		if !failIfMissing {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("Unable to load config file: %s", abs)
	}
	defer file.Close()
	props, err := LoadConfigurationFromReader(file, encoding)
	if err != nil {
		return nil, fmt.Errorf("Unable to load config file: %s: %w", abs, err)
	}
	return props, nil
}

// LoadConfigurationFromReader decodes using Java InputStreamReader semantics.
func LoadConfigurationFromReader(r io.Reader, encoding string) (map[string]string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text, err := DecodeScript(b, encoding, false)
	if err != nil {
		return nil, err
	}
	return LoadConfigurationFromString(text, false)
}

// DefaultConfigurationFiles returns the official four default config paths in
// precedence order (later files override earlier ones).
func DefaultConfigurationFiles(installationDir, workingDirectory string) []string {
	if installationDir == "" {
		installationDir = executableDir()
	}
	home, _ := os.UserHomeDir()
	files := []string{
		filepath.Join(installationDir, "conf", "flyway.conf"),
		filepath.Join(home, "flyway.conf"),
		"flyway.conf",
	}
	if workingDirectory != "" {
		files = append(files, filepath.Join(workingDirectory, "flyway.conf"))
	}
	return files
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// LoadDefaultConfigurationFiles loads the official default files in order.
func LoadDefaultConfigurationFiles(installationDir, workingDirectory, encoding string) (map[string]string, error) {
	out := make(map[string]string)
	for _, path := range DefaultConfigurationFiles(installationDir, workingDirectory) {
		props, err := LoadConfigurationFile(path, encoding, false)
		if err != nil {
			return nil, err
		}
		for k, v := range props {
			out[k] = v
		}
	}
	return out, nil
}

// RawEnvironmentVariables returns the process environment as a raw key/value
// map. Modern configuration differentiates upper-case Classic variables from
// lower-case TOML variables, so the converted map alone is insufficient.
func RawEnvironmentVariables() map[string]string {
	return environmentMap(os.Environ())
}

// FileExists reports whether path exists and is a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// AbsoluteFilePath returns the cleaned absolute form of path.
func AbsoluteFilePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

// DefaultTOMLConfigurationFiles returns the official Modern configuration file
// locations in precedence order. The working-directory layer is only present
// when workingDirectory was explicitly supplied.
func DefaultTOMLConfigurationFiles(installationDir, workingDirectory string) []string {
	if installationDir == "" {
		installationDir = executableDir()
	}
	home, _ := os.UserHomeDir()
	files := []string{
		filepath.Join(installationDir, "conf", "flyway.toml"),
		filepath.Join(installationDir, "conf", "flyway.user.toml"),
		filepath.Join(home, "flyway.toml"),
		filepath.Join(home, "flyway.user.toml"),
		"flyway.toml",
		"flyway.user.toml",
	}
	if workingDirectory != "" {
		files = append(files,
			filepath.Join(workingDirectory, "flyway.toml"),
			filepath.Join(workingDirectory, "flyway.user.toml"),
		)
	}
	return files
}

// LoadDefaultTOMLConfigurationFiles merges the existing default TOML files in
// official precedence order.
func LoadDefaultTOMLConfigurationFiles(installationDir, workingDirectory string) (ConfigurationModel, error) {
	return LoadTOMLConfigurationFiles(DefaultTOMLConfigurationFiles(installationDir, workingDirectory))
}

// ApplyConfiguration applies the ClassicConfiguration.configure subset relevant
// to the OSS PostgreSQL/MySQL implementation. Explicit false and zero values
// remain distinguishable from absent values.
func ApplyConfiguration(c *Config, props map[string]string) error {
	if c == nil {
		return fmt.Errorf("configuration must not be nil")
	}
	remaining := make(map[string]string, len(props))
	for k, v := range props {
		remaining[k] = v
	}
	for k, v := range props {
		if strings.HasPrefix(k, "environments.tempConfigEnvironment") {
			remaining["flyway"+strings.TrimPrefix(k, "environments.tempConfigEnvironment")] = v
			delete(remaining, k)
		}
	}

	takeString := func(key string) (string, bool) {
		v, ok := remaining[key]
		if ok {
			delete(remaining, key)
		}
		return v, ok
	}
	takeBool := func(key string) (*bool, error) {
		v, ok := remaining[key]
		if !ok {
			return nil, nil
		}
		delete(remaining, key)
		b, err := ParseBoolean(key, v)
		if err != nil {
			return nil, err
		}
		return &b, nil
	}
	takeInt := func(key string) (int, bool, error) {
		v, ok := remaining[key]
		if !ok {
			return 0, false, nil
		}
		delete(remaining, key)
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false, fmt.Errorf("Invalid value for %s (should be an integer): %s", key, v)
		}
		return n, true, nil
	}

	if v, ok := takeString("flyway.url"); ok {
		c.URL = v
	}
	if v, ok := takeString("flyway.user"); ok {
		c.User = v
	}
	if v, ok := takeString("flyway.password"); ok {
		c.Password = v
	}
	if v, ok := takeString("flyway.driver"); ok {
		c.Driver = v
	}
	if v, ok := takeString("flyway.initSql"); ok {
		c.InitSQL = v
	}
	if n, ok, err := takeInt("flyway.connectRetries"); err != nil {
		return err
	} else if ok {
		if n < 0 {
			return fmt.Errorf("Invalid number of connectRetries (must be 0 or greater): %d", n)
		}
		c.ConnectRetries = n
	}
	if n, ok, err := takeInt("flyway.connectRetriesInterval"); err != nil {
		return err
	} else if ok {
		if n < 0 {
			return fmt.Errorf("Invalid number for connectRetriesInterval (must be 0 or greater): %d", n)
		}
		c.ConnectRetriesInterval = &n
	}
	if v, ok := takeString("flyway.locations"); ok {
		c.Locations = splitComma(v)
	}
	if v, ok := takeString("flyway.callbackLocations"); ok {
		c.CallbackLocations = splitComma(v)
	}
	if v, ok := takeString("flyway.jarDirs"); ok {
		c.JarDirs = splitComma(v)
	}
	if v, ok := takeString("flyway.defaultSchema"); ok {
		if v == "" {
			c.DefaultSchema = ""
		} else {
			c.DefaultSchema = v
		}
	}
	if v, ok := takeString("flyway.schemas"); ok {
		c.Schemas = splitComma(v)
	}
	if v, ok := takeString("flyway.table"); ok {
		c.Table = v
	}
	if v, ok := takeString("flyway.baselineVersion"); ok {
		c.BaselineVersion = v
	}
	if v, ok := takeString("flyway.baselineDescription"); ok {
		c.BaselineDesc = v
	}
	if v, ok := takeString("flyway.baselineMigrationPrefix"); ok {
		c.Naming.BaselinePrefix = v
	}
	if b, err := takeBool("flyway.baselineOnMigrate"); err != nil {
		return err
	} else if b != nil {
		c.BaselineOnMigrate = *b
	}
	if b, err := takeBool("flyway.skipExecutingMigrations"); err != nil {
		return err
	} else if b != nil {
		c.SkipExecutingMigrations = *b
	}
	if b, err := takeBool("flyway.outOfOrder"); err != nil {
		return err
	} else if b != nil {
		c.OutOfOrder = *b
	}
	if b, err := takeBool("flyway.group"); err != nil {
		return err
	} else if b != nil {
		c.Group = *b
	}
	if b, err := takeBool("flyway.cleanDisabled"); err != nil {
		return err
	} else if b != nil {
		c.CleanDisabled = b
	}
	if b, err := takeBool("flyway.validateOnMigrate"); err != nil {
		return err
	} else if b != nil {
		c.ValidateOnMigrate = b
	}
	if b, err := takeBool("flyway.validateMigrationNaming"); err != nil {
		return err
	} else if b != nil {
		c.ValidateMigrationNaming = *b
	}
	if b, err := takeBool("flyway.mixed"); err != nil {
		return err
	} else if b != nil {
		c.Mixed = b
	}
	if b, err := takeBool("flyway.executeInTransaction"); err != nil {
		return err
	} else if b != nil {
		c.ExecuteInTransaction = b
	}
	if b, err := takeBool("flyway.placeholderReplacement"); err != nil {
		return err
	} else if b != nil {
		c.PlaceholderReplacement = b
	}
	if b, err := takeBool("flyway.createSchemas"); err != nil {
		return err
	} else if b != nil {
		c.CreateSchemas = b
	}
	if b, err := takeBool("flyway.failOnMissingLocations"); err != nil {
		return err
	} else if b != nil {
		c.FailOnMissingLocations = b
	}
	if b, err := takeBool("flyway.detectEncoding"); err != nil {
		return err
	} else if b != nil {
		c.DetectEncoding = b
	}
	if b, err := takeBool("flyway.postgresql.transactional.lock"); err != nil {
		return err
	} else if b != nil {
		c.PGTransactionalLock = b
	}

	if b, err := takeBool("flyway.skipDefaultCallbacks"); err != nil {
		return err
	} else if b != nil {
		c.SkipDefaultCallbacks = b
	}
	if v, ok := takeString("flyway.target"); ok {
		c.TargetVersion = strings.TrimSpace(v)
	}
	if v, ok := takeString("flyway.installedBy"); ok {
		c.InstalledBy = v
	}
	if v, ok := takeString("flyway.encoding"); ok {
		c.Encoding = v
		c.Naming.Encoding = v
	}
	if v, ok := takeString("flyway.placeholderPrefix"); ok {
		c.PlaceholderPrefix = v
	}
	if v, ok := takeString("flyway.placeholderSuffix"); ok {
		c.PlaceholderSuffix = v
	}
	if v, ok := takeString("flyway.placeholderSeparator"); ok {
		c.PlaceholderSeparator = v
	}
	if v, ok := takeString("flyway.scriptPlaceholderPrefix"); ok {
		c.ScriptPlaceholderPrefix = v
	}
	if v, ok := takeString("flyway.scriptPlaceholderSuffix"); ok {
		c.ScriptPlaceholderSuffix = v
	}
	if v, ok := takeString("flyway.sqlMigrationPrefix"); ok {
		c.Naming.VersionedPrefix = v
	}
	if v, ok := takeString("flyway.repeatableSqlMigrationPrefix"); ok {
		c.Naming.RepeatablePrefix = v
	}
	if v, ok := takeString("flyway.undoSqlMigrationPrefix"); ok {
		c.Naming.UndoPrefix = v
	}
	if v, ok := takeString("flyway.sqlMigrationSeparator"); ok {
		c.Naming.Separator = v
	}
	if v, ok := takeString("flyway.sqlMigrationSuffixes"); ok {
		c.Naming.Suffixes = splitComma(v)
	}
	if v, ok := takeString("flyway.ignoreMigrationPatterns"); ok {
		c.IgnoreMigrationPatterns = splitComma(v)
	}
	if n, ok, err := takeInt("flyway.lockRetryCount"); err != nil {
		return err
	} else if ok {
		c.LockRetryCount = &n
	}
	for key, value := range remaining {
		if strings.HasPrefix(key, "flyway.placeholders.") {
			if c.Placeholders == nil {
				c.Placeholders = make(map[string]string)
			}
			c.Placeholders[strings.TrimPrefix(key, "flyway.placeholders.")] = value
			delete(remaining, key)
		}
	}
	for key, value := range remaining {
		if strings.HasPrefix(key, "flyway.jdbcProperties.") {
			if c.JDBCProperties == nil {
				c.JDBCProperties = make(map[string]string)
			}
			c.JDBCProperties[strings.TrimPrefix(key, "flyway.jdbcProperties.")] = value
			delete(remaining, key)
		}
	}

	// These OSS configuration keys are accepted by ClassicConfiguration even
	// when this PostgreSQL/MySQL engine does not consume them.
	for _, key := range []string{
		"flyway.callbacks", "flyway.resolvers", "flyway.skipDefaultResolvers",
		"flyway.jarDirs", "flyway.loggers", "flyway.stream", "flyway.batch",
		"flyway.outputQueryResults",
		"flyway.communityDBSupportEnabled", "flyway.reportEnabled",
		"flyway.reportFilename", "flyway.dryRunOutput", "flyway.errorOverrides",
		"flyway.tablespace", "flyway.kerberosConfigFile", "flyway.configFiles",
		"flyway.configFileEncoding", "flyway.configurations", "flyway.licenseKey",
		"flyway.outputProgress", "flyway.outputLogsInJson", "flyway.outputType",
		"flyway.powershellExecutable", "flyway.migrationResolvers", "flyway.provisionMode", "flyway.color",
	} {
		delete(remaining, key)
	}
	unknown := make([]string, 0, len(remaining))
	for key := range remaining {
		unknown = append(unknown, key)
	}
	prefix := "flyway."
	return unknownConfigurationError(unknown, &prefix)
}

func splitComma(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// MergeConfiguration overlays higher-priority properties on lower-priority ones.
func MergeConfiguration(layers ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, layer := range layers {
		for key, value := range layer {
			out[key] = value
		}
	}
	return out
}
