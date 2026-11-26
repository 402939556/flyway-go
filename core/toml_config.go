package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ConfigMode selects the Flyway configuration model used for a run.
type ConfigMode int

const (
	ConfigModeClassic ConfigMode = iota
	ConfigModeModern
)

// ConfigurationModel is the nested Flyway TOML model. Maps are kept generic so
// unknown root/plugin namespaces survive a round-trip and merge.
type ConfigurationModel struct {
	Flyway             map[string]any
	Environments       map[string]map[string]any
	EnvironmentGroups  map[string]map[string]any
	RootConfigurations map[string]any
}

// DefaultConfigurationModel mirrors FlywayModel.defaults() and
// ConfigurationModel.defaults().
func DefaultConfigurationModel() ConfigurationModel {
	return ConfigurationModel{
		Flyway: map[string]any{
			"environment":                  "default",
			"outputProgress":               false,
			"reportFilename":               "report",
			"detectEncoding":               false,
			"encoding":                     "UTF-8",
			"executeInTransaction":         true,
			"placeholderPrefix":            "${",
			"placeholderSuffix":            "}",
			"placeholderSeparator":         ":",
			"scriptPlaceholderPrefix":      "FP__",
			"scriptPlaceholderSuffix":      "__",
			"sqlMigrationPrefix":           "V",
			"repeatableSqlMigrationPrefix": "R",
			"baselineMigrationPrefix":      "B",
			"sqlMigrationSeparator":        "__",
			"sqlMigrationSuffixes":         []string{".sql"},
			"cleanDisabled":                true,
			"communityDBSupportEnabled":    true,
			"locations":                    []string{"db/migration"},
			"callbackLocations":            []string{},
			"jarDirs":                      []string{},
			"target":                       "latest",
			"table":                        "flyway_schema_history",
			"placeholderReplacement":       true,
			"ignoreMigrationPatterns":      []string{"*:future"},
			"validateMigrationNaming":      false,
			"validateOnMigrate":            true,
			"baselineDescription":          "<< Flyway Baseline >>",
			"baselineOnMigrate":            false,
			"outOfOrder":                   false,
			"skipExecutingMigrations":      false,
			"callbacks":                    []string{},
			"skipDefaultCallbacks":         false,
			"migrationResolvers":           []string{},
			"skipDefaultResolvers":         false,
			"mixed":                        false,
			"group":                        false,
			"createSchemas":                true,
			"errorOverrides":               []string{},
			"stream":                       false,
			"batch":                        false,
			"outputQueryResults":           true,
			"lockRetryCount":               50,
			"kerberosConfigFile":           "",
			"failOnMissingLocations":       false,
			"loggers":                      []string{"auto"},
			"placeholders":                 map[string]any{},
			"provisionMode":                "provision",
			"reportEnabled":                false,
			"color":                        "auto",
		},
		Environments: map[string]map[string]any{
			"default": {"flyway": map[string]any{}},
		},
		EnvironmentGroups: map[string]map[string]any{
			"dev": {}, "test": {}, "staging": {}, "prod": {}, "shadow": {}, "build": {},
		},
		RootConfigurations: map[string]any{},
	}
}

// Clone returns a deep copy suitable for layered merge.
func (m ConfigurationModel) Clone() ConfigurationModel {
	return ConfigurationModel{
		Flyway:             cloneStringMap(m.Flyway),
		Environments:       cloneEnvironmentMap(m.Environments),
		EnvironmentGroups:  cloneEnvironmentMap(m.EnvironmentGroups),
		RootConfigurations: cloneStringMap(m.RootConfigurations),
	}
}

// Merge overlays override on the receiver using MergeUtils semantics.
func (m ConfigurationModel) Merge(override ConfigurationModel) ConfigurationModel {
	result := m.Clone()
	result.Flyway = mergeStringMaps(result.Flyway, override.Flyway)
	result.Environments = mergeEnvironmentMaps(result.Environments, override.Environments)
	result.EnvironmentGroups = mergeEnvironmentMaps(result.EnvironmentGroups, override.EnvironmentGroups)
	result.RootConfigurations = mergeStringMaps(result.RootConfigurations, override.RootConfigurations)
	return result
}

// LoadTOMLConfigurationFile decodes one Flyway TOML file.
func LoadTOMLConfigurationFile(path string) (ConfigurationModel, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ConfigurationModel{}, fmt.Errorf("Unable to load config file: %s: %w", absolutePath(path), err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(b, &raw); err != nil {
		return ConfigurationModel{}, fmt.Errorf("Error parsing config file %s: %w", absolutePath(path), err)
	}
	return NormalizeConfigurationModel(raw), nil
}

// LoadTOMLConfigurationFiles merges the existing TOML files in order.
func LoadTOMLConfigurationFiles(paths []string) (ConfigurationModel, error) {
	model := ConfigurationModel{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		next, err := LoadTOMLConfigurationFile(path)
		if err != nil {
			return ConfigurationModel{}, err
		}
		model = model.Merge(next)
	}
	return model, nil
}

// NormalizeConfigurationModel canonicalizes the known Flyway namespaces while
// preserving unknown root/plugin namespaces as generic maps.
func NormalizeConfigurationModel(raw map[string]any) ConfigurationModel {
	model := ConfigurationModel{
		Flyway:             map[string]any{},
		Environments:       map[string]map[string]any{},
		EnvironmentGroups:  map[string]map[string]any{},
		RootConfigurations: map[string]any{},
	}
	for key, value := range raw {
		switch strings.ToLower(key) {
		case "flyway":
			model.Flyway = normalizeFlywayMap(asStringMap(value))
		case "environments":
			for name, envValue := range asStringMap(value) {
				model.Environments[name] = normalizeEnvironmentMap(asStringMap(envValue))
			}
		case "environmentgroups":
			for name, groupValue := range asStringMap(value) {
				model.EnvironmentGroups[name] = cloneStringMap(asStringMap(groupValue))
			}
		default:
			model.RootConfigurations[key] = deepCopyValue(value)
		}
	}
	return model
}

// ConfigurationModelFromFlatMap converts dotted CLI/environment properties to
// the nested model and applies the string-to-type coercion performed by
// Jackson's Flyway model binding.
func ConfigurationModelFromFlatMap(properties map[string]string) (ConfigurationModel, error) {
	raw := map[string]any{}
	for key, value := range properties {
		parts := strings.Split(key, ".")
		if len(parts) == 0 {
			continue
		}
		coerced, err := coerceModelValue(parts, value)
		if err != nil {
			return ConfigurationModel{}, err
		}
		setNestedValue(raw, parts, coerced)
	}
	return NormalizeConfigurationModel(raw), nil
}

// ModernEnvironmentConfigurationFrom mirrors TomlUtils.loadConfigurationFromEnvironment.
func ModernEnvironmentConfigurationFrom(env map[string]string, knownEnvironmentNames []string) (ConfigurationModel, []string, error) {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	flat := map[string]string{}
	sources := map[string]string{}
	var warnings []string
	for _, key := range keys {
		if key == "FLYWAY_CONFIG_FILES" {
			continue
		}
		prop, ok, err := modernEnvironmentProperty(key, knownEnvironmentNames)
		if err != nil {
			return ConfigurationModel{}, warnings, err
		}
		if !ok {
			continue
		}
		if _, exists := flat[prop]; exists {

			chosen, old := key, sources[prop]
			if isClassicEnvironmentVariable(sources[prop]) {
				chosen, old = key, sources[prop]
			} else if isClassicEnvironmentVariable(key) {
				chosen, old = sources[prop], key
			} else if sources[prop] < key {
				chosen, old = sources[prop], key
			}
			warnings = append(warnings, fmt.Sprintf("Using both new Environment variable %s and old Environment variable %s. Please remove %s.", chosen, old, old))
			flat[prop] = env[chosen]
			sources[prop] = chosen
			continue
		}
		flat[prop] = env[key]
		sources[prop] = key
	}
	model, err := ConfigurationModelFromFlatMap(flat)
	return model, warnings, err
}

func modernEnvironmentProperty(key string, knownEnvironmentNames []string) (string, bool, error) {
	if key == "FLYWAY_CONFIG_FILES" {
		return "", false, nil
	}
	upper := strings.ToUpper(key)
	isClassic := strings.HasPrefix(key, "FLYWAY_") ||
		strings.HasPrefix(upper, "FLYWAY_JDBC_PROPERTIES_")
	isModernLower := strings.HasPrefix(key, "flyway_")
	hasEnvironmentPrefix := strings.HasPrefix(strings.ToLower(key), "environments_")
	if !isClassic && !isModernLower && !hasEnvironmentPrefix {
		return "", false, nil
	}
	if isClassic {
		prop := convertEnvironmentKey(upper)
		if prop == "" {
			return "", false, nil
		}
		return mapModernTemporaryEnvironment(prop), true, nil
	}
	resolved := key
	if hasEnvironmentPrefix {
		var err error
		resolved, err = resolveModernEnvironmentCasing(key, knownEnvironmentNames)
		if err != nil {
			return "", false, err
		}
	}
	prop := strings.ReplaceAll(resolved, "_", ".")
	return mapModernTemporaryEnvironment(prop), true, nil
}
func resolveModernEnvironmentCasing(key string, known []string) (string, error) {
	const prefix = "environments_"
	lowered := strings.ToLower(key)
	remainder := lowered[len(prefix):]
	candidate := remainder
	if i := strings.IndexByte(remainder, '_'); i >= 0 {
		candidate = remainder[:i]
	}
	matches := make([]string, 0)
	for _, name := range known {
		if strings.EqualFold(name, candidate) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	if len(matches) > 1 {
		return "", fmt.Errorf("Environment variable '%s' matches multiple environments that differ only by case: %s. Rename one of them, or set this value using its exact casing.", key, strings.Join(matches, ", "))
	}
	if len(matches) == 1 {
		return prefix + matches[0] + remainder[len(candidate):], nil
	}
	return lowered, nil
}

func mapModernTemporaryEnvironment(prop string) string {
	const prefix = "flyway."
	if !strings.HasPrefix(prop, prefix) {
		return prop
	}
	rest := strings.TrimPrefix(prop, prefix)
	first := rest
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		first = rest[:i]
	}
	if isEnvironmentField(first) {
		return "environments.tempConfigEnvironment." + rest
	}
	return prop
}

func isClassicEnvironmentVariable(key string) bool {
	return strings.HasPrefix(key, "FLYWAY_")
}

func isEnvironmentField(name string) bool {
	switch strings.ToLower(name) {
	case "url", "user", "password", "driver", "displayname", "schemas", "connectretries",
		"connectretriesinterval", "initsql", "jdbcproperties", "resolvers", "provisioner",
		"environmentgroups":
		return true
	default:
		return false
	}
}

// ToConfig resolves the active environment into the execution Config.
func (m ConfigurationModel) ToConfig(workingDirectory string) (Config, error) {
	return m.ToConfigWithInstallationDirectory(workingDirectory, "", nil)
}

// ToConfigWithInstallationDirectory resolves the active environment into the
// execution Config and applies the CLI's installation-directory-dependent
// defaults such as the default sql location.
func (m ConfigurationModel) ToConfigWithInstallationDirectory(
	workingDirectory string,
	installationDirectory string,
	warn func(format string, args ...interface{}),
) (Config, error) {
	m = m.MergeTemporaryEnvironment().ResolveRelativePaths(workingDirectory)

	environmentName := "default"
	if value, ok := m.Flyway["environment"]; ok && value != nil {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			environmentName = text
		}
	}
	environment, ok := m.Environments[environmentName]
	if !ok {
		return Config{}, m.EnvironmentNotFoundError(environmentName)
	}

	cfg := Config{
		Mode:             ConfigModeModern,
		WorkingDirectory: workingDirectory,
		EnvironmentName:  environmentName,
	}
	rootFlyway := cloneStringMap(m.Flyway)
	delete(rootFlyway, "environment")
	if err := ApplyConfiguration(&cfg, flattenFlywayMap("flyway", rootFlyway)); err != nil {
		return Config{}, err
	}
	if flyway, ok := environment["flyway"]; ok {
		if err := ApplyConfiguration(&cfg, flattenFlywayMap("flyway", asStringMap(flyway))); err != nil {
			return Config{}, err
		}
	}
	if err := applyModernEnvironmentFields(&cfg, environment, environmentName); err != nil {
		return Config{}, err
	}
	cfg.Mode = ConfigModeModern
	cfg.WorkingDirectory = workingDirectory
	cfg.ApplyDefaultsModern()
	ApplyDefaultSQLocation(&cfg, installationDirectory, warn)
	return cfg, nil
}

func applyModernEnvironmentFields(cfg *Config, environment map[string]any, environmentName string) error {
	if value, ok := environment["url"]; ok {
		cfg.URL = stringValue(value)
	}
	if value, ok := environment["user"]; ok {
		cfg.User = stringValue(value)
	}
	if value, ok := environment["password"]; ok {
		cfg.Password = stringValue(value)
	}
	if value, ok := environment["driver"]; ok {
		cfg.Driver = stringValue(value)
	}
	if value, ok := environment["displayName"]; ok {
		cfg.DisplayName = stringValue(value)
	}
	if value, ok := environment["initSql"]; ok {
		cfg.InitSQL = stringValue(value)
	}
	if value, ok := environment["schemas"]; ok {
		cfg.Schemas = stringListValue(value)
	}
	if value, ok := environment["connectRetries"]; ok {
		n, err := intValue(value)
		if err != nil {
			return fmt.Errorf("Invalid value for environments.%s.connectRetries: %w", environmentName, err)
		}
		if n < 0 {
			return fmt.Errorf("Invalid number of connectRetries (must be 0 or greater): %d", n)
		}
		cfg.ConnectRetries = n
	}
	if value, ok := environment["connectRetriesInterval"]; ok {
		n, err := intValue(value)
		if err != nil {
			return fmt.Errorf("Invalid value for environments.%s.connectRetriesInterval: %w", environmentName, err)
		}
		if n < 0 {
			return fmt.Errorf("Invalid number for connectRetriesInterval (must be 0 or greater): %d", n)
		}
		cfg.ConnectRetriesInterval = &n
	}
	if value, ok := environment["jdbcProperties"]; ok {
		props := asStringMap(value)
		if len(props) > 0 && cfg.JDBCProperties == nil {
			cfg.JDBCProperties = map[string]string{}
		}
		for key, item := range props {
			cfg.JDBCProperties[key] = stringValue(item)
		}
	}
	if value, ok := environment["resolvers"]; ok {
		cfg.Resolvers = resolverStringMap(value)
	}
	if value, ok := environment["provisioner"]; ok {
		cfg.Provisioner = stringValue(value)
	}
	if value, ok := environment["environmentGroups"]; ok {
		cfg.EnvironmentGroups = stringListValue(value)
	}
	return nil
}

func resolverStringMap(value any) map[string]map[string]string {
	raw := asStringMap(value)
	out := make(map[string]map[string]string, len(raw))
	for name, resolverValue := range raw {
		resolver := asStringMap(resolverValue)
		values := make(map[string]string, len(resolver))
		for key, item := range resolver {
			values[key] = stringValue(item)
		}
		out[name] = values
	}
	return out
}
func flattenFlywayMap(prefix string, value map[string]any) map[string]string {
	out := map[string]string{}
	flattenValue(prefix, value, out)
	return out
}

func flattenValue(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case nil:
		return
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenValue(prefix+"."+key, typed[key], out)
		}
	case map[string]string:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenValue(prefix+"."+key, typed[key], out)
		}
	case []string:
		out[prefix] = strings.Join(typed, ",")
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, fmt.Sprint(item))
		}
		out[prefix] = strings.Join(items, ",")
	default:
		out[prefix] = fmt.Sprint(typed)
	}
}

func coerceModelValue(parts []string, value string) (any, error) {
	last := parts[len(parts)-1]
	if isModelListField(last) {
		return splitComma(value), nil
	}
	if isModelBoolField(last) {
		b, err := ParseBoolean(strings.Join(parts, "."), value)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	if isModelIntField(last) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("Invalid value for %s (should be an integer): %s", strings.Join(parts, "."), value)
		}
		return n, nil
	}
	return value, nil
}

func isModelListField(name string) bool {
	switch strings.ToLower(name) {
	case "locations", "callbacklocations", "jardirs", "sqlmigrationsuffixes",
		"ignoremigrationpatterns", "callbacks", "migrationresolvers", "erroroverrides",
		"loggers", "schemas", "environmentgroups":
		return true
	default:
		return false
	}
}

func isModelBoolField(name string) bool {
	switch strings.ToLower(name) {
	case "outputprogress", "outputlogsinjson", "detectencoding", "executeintransaction",
		"placeholderreplacement", "validatemigrationnaming", "validateonmigrate",
		"baselineonmigrate", "outoforder", "skipexecutingmigrations", "skipdefaultcallbacks",
		"skipdefaultresolvers", "mixed", "group", "createschemas", "stream", "batch",
		"outputqueryresults", "failonmissinglocations", "reportenabled":
		return true
	default:
		return false
	}
}

func isModelIntField(name string) bool {
	switch strings.ToLower(name) {
	case "connectretries", "connectretriesinterval", "lockretrycount":
		return true
	default:
		return false
	}
}

func normalizeFlywayMap(raw map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range raw {
		canonical := canonicalFlywayKey(key)
		if strings.EqualFold(canonical, "placeholders") {
			out[canonical] = normalizeStringMap(value)
			continue
		}
		switch strings.ToLower(canonical) {
		case "locations", "callbacklocations", "jardirs", "sqlmigrationsuffixes", "ignoremigrationpatterns", "callbacks", "migrationresolvers", "erroroverrides", "loggers":
			out[canonical] = normalizeList(value)
		case "connectretries", "connectretriesinterval", "lockretrycount":
			if n, err := intValue(value); err == nil {
				out[canonical] = n
			} else {
				out[canonical] = value
			}
		default:
			out[canonical] = deepCopyValue(value)
		}
	}
	return out
}

func normalizeEnvironmentMap(raw map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range raw {
		canonical := canonicalEnvironmentKey(key)
		switch strings.ToLower(canonical) {
		case "flyway":
			out[canonical] = normalizeFlywayMap(asStringMap(value))
		case "schemas", "environmentgroups":
			out[canonical] = normalizeList(value)
		case "connectretries", "connectretriesinterval":
			if n, err := intValue(value); err == nil {
				out[canonical] = n
			} else {
				out[canonical] = value
			}
		case "jdbcproperties":
			out[canonical] = normalizeStringMap(value)
		case "resolvers":
			out[canonical] = normalizeNestedStringMap(value)
		default:
			out[canonical] = deepCopyValue(value)
		}
	}
	return out
}

func canonicalFlywayKey(key string) string {
	canonical := map[string]string{
		"reportfilename": "reportFilename", "detectencoding": "detectEncoding", "encoding": "encoding",
		"placeholderprefix": "placeholderPrefix", "placeholdersuffix": "placeholderSuffix",
		"placeholderseparator": "placeholderSeparator", "scriptplaceholderprefix": "scriptPlaceholderPrefix",
		"scriptplaceholdersuffix": "scriptPlaceholderSuffix", "powershellexecutable": "powershellExecutable",
		"sqlmigrationprefix": "sqlMigrationPrefix", "executeintransaction": "executeInTransaction",
		"repeatablesqlmigrationprefix": "repeatableSqlMigrationPrefix", "sqlmigrationseparator": "sqlMigrationSeparator",
		"sqlmigrationsuffixes": "sqlMigrationSuffixes", "cleandisabled": "cleanDisabled",
		"communitydbsupportenabled": "communityDBSupportEnabled", "locations": "locations",
		"callbacklocations": "callbackLocations", "jardirs": "jarDirs", "table": "table",
		"tablespace": "tablespace", "target": "target",
		"placeholderreplacement": "placeholderReplacement", "ignoremigrationpatterns": "ignoreMigrationPatterns",
		"validatemigrationnaming": "validateMigrationNaming", "validateonmigrate": "validateOnMigrate",
		"baselineversion": "baselineVersion", "baselinedescription": "baselineDescription",
		"baselinemigrationprefix": "baselineMigrationPrefix",
		"baselineonmigrate":       "baselineOnMigrate", "outoforder": "outOfOrder",
		"skipexecutingmigrations": "skipExecutingMigrations", "callbacks": "callbacks",
		"skipdefaultcallbacks": "skipDefaultCallbacks", "migrationresolvers": "migrationResolvers",
		"skipdefaultresolvers": "skipDefaultResolvers", "mixed": "mixed", "group": "group",
		"installedby": "installedBy", "createschemas": "createSchemas", "erroroverrides": "errorOverrides",
		"dryrunoutput": "dryRunOutput", "stream": "stream", "batch": "batch",
		"outputqueryresults": "outputQueryResults", "lockretrycount": "lockRetryCount",
		"kerberosconfigfile": "kerberosConfigFile", "failonmissinglocations": "failOnMissingLocations",
		"loggers": "loggers", "placeholders": "placeholders", "defaultschema": "defaultSchema",
		"reportenabled": "reportEnabled", "environment": "environment", "outputtype": "outputType",
		"outputprogress": "outputProgress", "outputlogsinjson": "outputLogsInJson", "provisionmode": "provisionMode",
		"color": "color",
	}
	if value, ok := canonical[strings.ToLower(key)]; ok {
		return value
	}
	return key
}

func canonicalEnvironmentKey(key string) string {
	canonical := map[string]string{
		"url": "url", "user": "user", "password": "password", "driver": "driver",
		"displayname": "displayName", "schemas": "schemas", "connectretries": "connectRetries",
		"connectretriesinterval": "connectRetriesInterval", "initsql": "initSql",
		"jdbcproperties": "jdbcProperties", "resolvers": "resolvers", "provisioner": "provisioner",
		"environmentgroups": "environmentGroups", "flyway": "flyway",
	}
	if value, ok := canonical[strings.ToLower(key)]; ok {
		return value
	}
	return key
}

func mergeStringMaps(lower, upper map[string]any) map[string]any {
	if lower == nil {
		return cloneStringMap(upper)
	}
	out := cloneStringMap(lower)
	for key, upperValue := range upper {
		if lowerValue, ok := out[key]; ok {
			out[key] = mergeModelValues(lowerValue, upperValue)
		} else {
			out[key] = deepCopyValue(upperValue)
		}
	}
	return out
}

func mergeEnvironmentMaps(lower, upper map[string]map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for key, value := range lower {
		out[key] = cloneStringMap(value)
	}
	for key, value := range upper {
		if existing, ok := out[key]; ok {
			out[key] = mergeStringMaps(existing, value)
		} else {
			out[key] = cloneStringMap(value)
		}
	}
	return out
}

func mergeModelValues(lower, upper any) any {
	if upper == nil {
		return deepCopyValue(lower)
	}
	if isModelList(upper) && modelListLen(upper) == 0 {
		return deepCopyValue(lower)
	}
	lowerMap, lowerOK := asMap(lower)
	upperMap, upperOK := asMap(upper)
	if lowerOK && upperOK {
		return mergeStringMaps(lowerMap, upperMap)
	}
	return deepCopyValue(upper)
}

func isModelList(value any) bool {
	switch value.(type) {
	case []any, []string:
		return true
	default:
		return false
	}
}

func modelListLen(value any) int {
	switch typed := value.(type) {
	case []any:
		return len(typed)
	case []string:
		return len(typed)
	default:
		return 0
	}
}

func cloneStringMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = deepCopyValue(value)
	}
	return out
}

func cloneEnvironmentMap(src map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(src))
	for key, value := range src {
		out[key] = cloneStringMap(value)
	}
	return out
}

func deepCopyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringMap(typed)
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = deepCopyValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func asStringMap(value any) map[string]any {
	typed, _ := asMap(value)
	if typed == nil {
		return map[string]any{}
	}
	return typed
}

func normalizeStringMap(value any) map[string]any {
	raw := asStringMap(value)
	out := make(map[string]any, len(raw))
	for key, item := range raw {
		out[key] = deepCopyValue(item)
	}
	return out
}

func normalizeNestedStringMap(value any) map[string]any {
	raw := asStringMap(value)
	out := make(map[string]any, len(raw))
	for key, item := range raw {
		out[key] = normalizeStringMap(item)
	}
	return out
}

func normalizeList(value any) []any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = deepCopyValue(item)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out
	default:
		items := splitComma(fmt.Sprint(value))
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = item
		}
		return out
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func stringListValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return splitComma(fmt.Sprint(value))
	}
}

func intValue(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("not an integer: %v", typed)
		}
		return int(typed), nil
	case string:
		return strconv.Atoi(typed)
	default:
		return 0, fmt.Errorf("not an integer: %v", value)
	}
}

func setNestedValue(root map[string]any, parts []string, value any) {
	current := root
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
}

func absolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
