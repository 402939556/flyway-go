package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linsu/flywaygo/core"
)

type preparedCLIArguments struct {
	flagArgs        []string
	modernArgs      map[string]string
	classicArgs     map[string]string
	environmentArgs map[string]map[string]string
}

type configurationSelection struct {
	mode                   core.ConfigMode
	explicitToml           []string
	explicitLegacy         []string
	environmentToml        []string
	environmentLegacy      []string
	defaultToml            []string
	defaultLegacy          []string
	existingExplicitToml   []string
	existingExplicitLegacy []string
}

func defaultInstallationDirectory() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func dependencyInstallationDirectory(deps cliDependencies) string {
	directory := ""
	if deps.installationDirectory != nil {
		directory = strings.TrimSpace(deps.installationDirectory())
	}
	if directory == "" {
		directory = defaultInstallationDirectory()
	}
	return filepath.Clean(directory)
}

func dependencyEnvironment(deps cliDependencies) map[string]string {
	if deps.environment == nil {
		return map[string]string{}
	}
	return deps.environment()
}

func dependencyRawEnvironment(deps cliDependencies) map[string]string {
	if deps.rawEnvironment != nil {
		return deps.rawEnvironment()
	}
	if deps.environment == nil {
		return map[string]string{}
	}
	env := deps.environment()
	raw := make(map[string]string, len(env))
	for key, value := range env {
		raw[key] = value
	}
	return raw
}

func dependencyFileExists(deps cliDependencies, path string) bool {
	if deps.fileExists != nil {
		return deps.fileExists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

func warnf(deps cliDependencies, stderr io.Writer, format string, args ...interface{}) {
	if deps.warn != nil {
		deps.warn(format, args...)
		return
	}
	fmt.Fprintf(stderr, "[WARN] "+format+"\n", args...)
}

// prepareCLIConfigurationArgs separates Flyway's generic -key=value
// configuration arguments from flags consumed by Go's flag package. The
// project also keeps the older -key value compatibility path.
func prepareCLIConfigurationArgs(args []string) (preparedCLIArguments, error) {
	prepared := preparedCLIArguments{
		flagArgs:        make([]string, 0, len(args)),
		modernArgs:      map[string]string{},
		classicArgs:     map[string]string{},
		environmentArgs: map[string]map[string]string{},
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			prepared.flagArgs = append(prepared.flagArgs, arg)
			continue
		}
		equals := strings.IndexByte(arg, '=')
		if equals < 0 {
			prepared.flagArgs = append(prepared.flagArgs, arg)
			continue
		}
		name := arg[1:equals]
		value := arg[equals+1:]
		lowerName := strings.ToLower(name)
		if lowerName == "configfiles" || lowerName == "configfileencoding" {
			prepared.flagArgs = append(prepared.flagArgs, arg)
			continue
		}
		if strings.HasPrefix(lowerName, "environments.") {
			if err := addEnvironmentArgument(prepared.environmentArgs, name, value); err != nil {
				return preparedCLIArguments{}, err
			}
			continue
		}
		if isCommandLineOnlyConfiguration(name) {
			prepared.flagArgs = append(prepared.flagArgs, arg)
			continue
		}

		modern, classic := modelPropertiesForArgument(name, value)
		for key, item := range modern {
			prepared.modernArgs[key] = item
		}
		for key, item := range classic {
			prepared.classicArgs[key] = item
		}
		// Known flags still go through flag.Parse so invalid booleans and values
		// retain the CLI's established parse errors. Unknown configuration
		// arguments are consumed here and validated by the configuration model.
		if flagPropertyName(name) != "" {
			prepared.flagArgs = append(prepared.flagArgs, arg)
		}
	}
	return prepared, nil
}

func addEnvironmentArgument(target map[string]map[string]string, name, value string) error {
	const prefix = "environments."
	remainder := name[len(prefix):]
	dot := strings.IndexByte(remainder, '.')
	if dot <= 0 || dot == len(remainder)-1 {
		return fmt.Errorf("Invalid configuration argument: -%s. Both the environment name and configuration option are required.", name)
	}
	environmentName := remainder[:dot]
	configurationKey := remainder[dot+1:]
	if strings.HasPrefix(strings.ToLower(configurationKey), "resolvers.") && len(strings.Split(configurationKey, ".")) != 3 {
		return fmt.Errorf("Invalid resolver configuration for environment %s: %s", environmentName, configurationKey)
	}
	if !isDynamicEnvironmentKey(configurationKey) {
		return fmt.Errorf("Failed to configure parameter: '%s' in your '%s' environment", configurationKey, environmentName)
	}
	values := target[environmentName]
	if values == nil {
		values = map[string]string{}
		target[environmentName] = values
	}
	values[configurationKey] = value
	return nil
}

func isDynamicEnvironmentKey(key string) bool {
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "jdbcproperties.") || strings.HasPrefix(lower, "flyway.") || strings.HasPrefix(lower, "resolvers.") {
		return true
	}
	if strings.Contains(key, ".") {
		return false
	}
	switch lower {
	case "url", "user", "password", "driver", "displayname", "schemas",
		"connectretries", "connectretriesinterval", "initsql", "jdbcproperties",
		"resolvers", "provisioner", "environmentgroups":
		return true
	default:
		return false
	}
}

func isCommandLineOnlyConfiguration(name string) bool {
	switch strings.ToLower(name) {
	case "outputfile", "workingdirectory", "infosincedate", "infountildate",
		"infosinceversion", "infountilversion", "infoofstate":
		return true
	default:
		return false
	}
}

func modelPropertiesForArgument(name, value string) (map[string]string, map[string]string) {
	modern := map[string]string{}
	classic := map[string]string{}

	if strings.EqualFold(name, "placeholders") {
		for key, item := range parsePlaceholderAssignments(value) {
			modern["flyway.placeholders."+key] = item
			classic["flyway.placeholders."+key] = item
		}
		return modern, classic
	}

	property := flagPropertyName(name)
	if property != "" {
		property = strings.TrimPrefix(property, "flyway.")
	} else {
		property = name
	}
	if property == "" || isCommandLineOnlyConfiguration(property) {
		return modern, classic
	}
	classic["flyway."+property] = value
	first := property
	if dot := strings.IndexByte(first, '.'); dot >= 0 {
		first = first[:dot]
	}
	if core.IsEnvironmentField(first) {
		modern["environments.tempConfigEnvironment."+property] = value
	} else {
		modern["flyway."+property] = value
	}
	return modern, classic
}

func modelFromFlatProperties(properties map[string]string) (core.ConfigurationModel, error) {
	if len(properties) == 0 {
		return core.ConfigurationModel{}, nil
	}
	return core.ConfigurationModelFromFlatMap(properties)
}

func modelFromDynamicEnvironmentArgs(environmentArgs map[string]map[string]string) (core.ConfigurationModel, error) {
	flat := map[string]string{}
	for environmentName, values := range environmentArgs {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flat["environments."+environmentName+"."+key] = values[key]
		}
	}
	model, err := modelFromFlatProperties(flat)
	if err != nil {
		return core.ConfigurationModel{}, err
	}
	if model.Environments == nil {
		model.Environments = map[string]map[string]any{}
	}
	for environmentName := range environmentArgs {
		if model.Environments[environmentName] == nil {
			model.Environments[environmentName] = map[string]any{}
		}
	}
	return model, nil
}

func modernExplicitFlagProperties(fs *flag.FlagSet) map[string]string {
	properties := map[string]string{}
	if fs == nil {
		return properties
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "working-directory" || f.Name == "workingDirectory" || f.Name == "configFiles" || f.Name == "configFileEncoding" {
			return
		}
		modern, _ := modelPropertiesForArgument(f.Name, f.Value.String())
		for key, value := range modern {
			properties[key] = value
		}
	})
	return properties
}

func configurationFilePaths(raw, workingDirectory string) []string {
	paths := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if workingDirectory != "" && !filepath.IsAbs(item) {
			item = filepath.Join(workingDirectory, item)
		}
		paths = append(paths, core.AbsoluteFilePath(item))
	}
	return paths
}

func configFilePathsForMode(raw, workingDirectory string, toml bool) []string {
	paths := make([]string, 0)
	for _, item := range configurationFilePaths(raw, workingDirectory) {
		if toml != strings.HasSuffix(item, ".toml") {
			continue
		}
		paths = append(paths, item)
	}
	return paths
}

func selectConfigurationMode(deps cliDependencies, explicitRaw, workingDirectory string, stderr io.Writer) (configurationSelection, error) {
	installDirectory := dependencyInstallationDirectory(deps)
	rawEnvironment := dependencyRawEnvironment(deps)
	environmentRaw := ""
	if value, ok := rawEnvironment["FLYWAY_CONFIG_FILES"]; ok {
		environmentRaw = value
	}

	selection := configurationSelection{}
	selection.explicitToml = configFilePathsForMode(explicitRaw, workingDirectory, true)
	selection.explicitLegacy = configFilePathsForMode(explicitRaw, workingDirectory, false)
	selection.environmentToml = configFilePathsForMode(environmentRaw, workingDirectory, true)
	selection.environmentLegacy = configFilePathsForMode(environmentRaw, workingDirectory, false)

	var missing []string
	for _, path := range configurationFilePaths(explicitRaw, workingDirectory) {
		if dependencyFileExists(deps, path) {
			if strings.HasSuffix(path, ".toml") {
				selection.existingExplicitToml = append(selection.existingExplicitToml, path)
			} else {
				selection.existingExplicitLegacy = append(selection.existingExplicitLegacy, path)
			}
		} else {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		warnf(deps, stderr, "One or more specified configuration files could not be found: %s", strings.Join(missing, ", "))
	}

	modernAvailable := deps.loadDefaultTOML != nil || deps.loadTOMLFiles != nil
	if !modernAvailable {
		selection.mode = core.ConfigModeClassic
		return selection, nil
	}

	if len(selection.existingExplicitToml) > 0 && len(selection.existingExplicitLegacy) > 0 {
		return configurationSelection{}, coexistenceError(selection.existingExplicitToml, selection.existingExplicitLegacy)
	}
	if len(selection.existingExplicitToml) > 0 {
		selection.mode = core.ConfigModeModern
		return selection, nil
	}
	if len(selection.existingExplicitLegacy) > 0 {
		selection.mode = core.ConfigModeClassic
		return selection, nil
	}

	// Environment-supplied file lists are classified before checking whether
	// the files exist, matching ConfigurationManagerImpl.
	if len(selection.environmentToml) > 0 && len(selection.environmentLegacy) > 0 {
		return configurationSelection{}, coexistenceError(selection.environmentToml, selection.environmentLegacy)
	}
	if len(selection.environmentToml) > 0 {
		selection.mode = core.ConfigModeModern
		return selection, nil
	}
	if len(selection.environmentLegacy) > 0 {
		selection.mode = core.ConfigModeClassic
		return selection, nil
	}

	for _, path := range core.DefaultTOMLConfigurationFiles(installDirectory, workingDirectory) {
		if dependencyFileExists(deps, path) {
			selection.defaultToml = append(selection.defaultToml, path)
		}
	}
	for _, path := range core.DefaultConfigurationFiles(installDirectory, workingDirectory) {
		if dependencyFileExists(deps, path) {
			selection.defaultLegacy = append(selection.defaultLegacy, path)
		}
	}
	switch {
	case len(selection.defaultToml) > 0:
		selection.mode = core.ConfigModeModern
	case len(selection.defaultLegacy) > 0:
		selection.mode = core.ConfigModeClassic
	default:
		selection.mode = core.ConfigModeModern
	}
	return selection, nil
}

func coexistenceError(tomlFiles, legacyFiles []string) error {
	return fmt.Errorf("Using both TOML configuration and CONF configuration is not supported. Please remove the CONF configuration files.\nTOML files: %s\nCONF files: %s", strings.Join(tomlFiles, ", "), strings.Join(legacyFiles, ", "))
}

func resolveModernInvocation(
	command string,
	fs *flag.FlagSet,
	values *cliFlags,
	prepared preparedCLIArguments,
	selection configurationSelection,
	stdout io.Writer,
	stderr io.Writer,
	deps cliDependencies,
) (cliInvocation, int, bool) {
	workingDirectory := normalizedWorkingDirectory(values.workingDirectory)
	installDirectory := dependencyInstallationDirectory(deps)
	rawEnvironment := dependencyRawEnvironment(deps)

	model := core.DefaultConfigurationModel()
	if deps.loadDefaultTOML != nil {
		loaded, err := deps.loadDefaultTOML(installDirectory, workingDirectory)
		if err != nil {
			writeError(stderr, err)
			return cliInvocation{}, 1, true
		}
		model = model.Merge(loaded)
	}
	if deps.loadTOMLFiles != nil {
		files := append([]string{}, selection.defaultToml...)
		files = append(files, selection.environmentToml...)
		files = append(files, selection.explicitToml...)
		loaded, err := deps.loadTOMLFiles(files)
		if err != nil {
			writeError(stderr, err)
			return cliInvocation{}, 1, true
		}
		model = model.Merge(loaded)
	}

	knownEnvironments := make([]string, 0, len(model.Environments))
	for name := range model.Environments {
		knownEnvironments = append(knownEnvironments, name)
	}
	environmentModel, warnings, err := core.ModernEnvironmentConfigurationFrom(rawEnvironment, knownEnvironments)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	for _, warning := range warnings {
		warnf(deps, stderr, "%s", warning)
	}
	model = model.Merge(environmentModel)

	argumentModel, err := modelFromFlatProperties(prepared.modernArgs)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	model = model.Merge(argumentModel)

	dynamicModel, err := modelFromDynamicEnvironmentArgs(prepared.environmentArgs)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	model = model.Merge(dynamicModel)

	explicitFlagModel, err := modelFromFlatProperties(modernExplicitFlagProperties(fs))
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	model = model.Merge(explicitFlagModel)

	model, err = core.ResolveConfigurationModelProperties(model, rawEnvironment)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}

	derivedInstallDirectory := installDirectory
	if fs != nil && flagWasSet(fs, "workingDirectory") {
		derivedInstallDirectory = workingDirectory
	} else if fs != nil && flagWasSet(fs, "working-directory") {
		derivedInstallDirectory = workingDirectory
	}
	warn := func(format string, args ...interface{}) {
		warnf(deps, stderr, format, args...)
	}
	cfg, err := model.ToConfigWithInstallationDirectory(workingDirectory, derivedInstallDirectory, warn)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	addDerivedJarDirectory(&cfg, derivedInstallDirectory, warn)

	return finishInvocation(command, cfg, stdout, stderr)
}

func resolveClassicInvocation(
	command string,
	fs *flag.FlagSet,
	values *cliFlags,
	prepared preparedCLIArguments,
	selection configurationSelection,
	stdout io.Writer,
	stderr io.Writer,
	deps cliDependencies,
) (cliInvocation, int, bool) {
	workingDirectory := normalizedWorkingDirectory(values.workingDirectory)
	installDirectory := dependencyInstallationDirectory(deps)
	derivedInstallDirectory := installDirectory
	if fs != nil && (flagWasSet(fs, "workingDirectory") || flagWasSet(fs, "working-directory")) {
		derivedInstallDirectory = workingDirectory
	}
	environment := dependencyEnvironment(deps)

	encoding := "UTF-8"
	if value, ok := environment["flyway.configFileEncoding"]; ok {
		encoding = value
	} else if fs != nil && flagWasSet(fs, "configFileEncoding") {
		encoding = values.configFileEncoding
	}

	cfg := core.Config{WorkingDirectory: workingDirectory, Mode: core.ConfigModeClassic}
	jarDir := filepath.Join(derivedInstallDirectory, "jars")
	warnIfDeprecatedMigrationsFolder(jarDir, ".jar", func(format string, args ...interface{}) {
		warnf(deps, stderr, format, args...)
	})
	if dependencyFileExists(deps, jarDir) {
		cfg.JarDirs = []string{core.AbsoluteFilePath(jarDir)}
	}

	if deps.loadDefaultConfig != nil {
		defaults, err := deps.loadDefaultConfig(installDirectory, workingDirectory, encoding)
		if err != nil {
			writeError(stderr, err)
			return cliInvocation{}, 1, true
		}
		if err := applyConfigurationProperties(&cfg, defaults); err != nil {
			writeError(stderr, err)
			return cliInvocation{}, 1, true
		}
	}

	if deps.loadConfigFile != nil {
		files := configFilePaths(environment["flyway.configFiles"], workingDirectory)
		files = append(files, configFilePaths(values.configFiles, workingDirectory)...)
		for _, path := range files {
			props, err := deps.loadConfigFile(path, encoding, true)
			if err != nil {
				writeError(stderr, err)
				return cliInvocation{}, 1, true
			}
			if err := applyConfigurationProperties(&cfg, props); err != nil {
				writeError(stderr, err)
				return cliInvocation{}, 1, true
			}
		}
	}

	if err := applyConfigurationProperties(&cfg, environment); err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	if err := core.ApplyConfiguration(&cfg, prepared.classicArgs); err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	if err := core.ApplyConfiguration(&cfg, explicitFlagProperties(fs)); err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	if fs != nil && flagWasSet(fs, "ignore-missing") {
		cfg.IgnoreMissing = values.ignoreMissing
	}
	if fs != nil && flagWasSet(fs, "ignore-pending") {
		cfg.IgnorePending = values.ignorePending
	}
	cfg.ApplyDefaultsClassic()
	core.ApplyDefaultSQLocation(&cfg, derivedInstallDirectory, func(format string, args ...interface{}) {
		warnf(deps, stderr, format, args...)
	})
	return finishInvocation(command, cfg, stdout, stderr)
}

func normalizedWorkingDirectory(raw string) string {
	workingDirectory := strings.TrimSpace(raw)
	if workingDirectory == "" {
		return ""
	}
	return filepath.Clean(core.AbsoluteFilePath(workingDirectory))
}

func addDerivedJarDirectory(cfg *core.Config, installationDirectory string, warn func(format string, args ...interface{})) {
	if cfg == nil || strings.TrimSpace(installationDirectory) == "" {
		return
	}
	jarDir := filepath.Join(installationDirectory, "jars")
	warnIfDeprecatedMigrationsFolder(jarDir, ".jar", warn)
	if _, err := os.Stat(jarDir); err != nil {
		return
	}
	absolute := core.AbsoluteFilePath(jarDir)
	cfg.JarDirs = append([]string{absolute}, cfg.JarDirs...)
}

func warnIfDeprecatedMigrationsFolder(folder, extension string, warn func(format string, args ...interface{})) {
	if warn == nil {
		return
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), extension) {
			warn("Storing migrations in '%s' is not recommended and default scanning of this location may be deprecated in a future release", filepath.Base(folder))
			return
		}
	}
}

func finishInvocation(command string, cfg core.Config, stdout, stderr io.Writer) (cliInvocation, int, bool) {
	cfg.ApplyDefaults()
	if strings.TrimSpace(cfg.URL) == "" {
		fmt.Fprintln(stderr, "错误: 必须通过 -url 指定数据库连接串")
		fmt.Fprintln(stderr)
		fmt.Fprint(stderr, usage)
		return cliInvocation{}, 2, true
	}
	drv, dsn, err := parseURLWithCredentials(cfg.URL, cfg.Driver, cfg.User, cfg.Password)
	if err != nil {
		writeError(stderr, err)
		return cliInvocation{}, 1, true
	}
	cfg.Driver = drv
	cfg.DSN = dsn
	return cliInvocation{Command: command, Config: cfg, Driver: drv, DSN: dsn}, 0, false
}
