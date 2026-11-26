package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linsu/flywaygo/core"
)

const redgateEditionDownloadURL = "https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=redgate-edition-download"

type userCLIRuntime struct {
	stdout         io.Writer
	stderr         io.Writer
	deps           cliDependencies
	logger         *userCLILogger
	quiet          bool
	debug          bool
	debugRequested bool
	outputJSON     bool
	outputFile     string
	bannerPrinted  bool
}

const (
	userCLIJavaVersion = "21.0.9+10-LTS"
	userCLIJavaRuntime = "Java 21.0.9 (Microsoft)"
	userCLIOSRuntime   = "Windows 11 10.0 amd64"
)

// runUserCLI is the official-compatible command-line entry point. The old
// run/runWithDependencies harness remains available for library-oriented tests.
func runUserCLI(args []string, stdout, stderr io.Writer, deps cliDependencies) int {
	runtime := &userCLIRuntime{stdout: stdout, stderr: stderr, deps: deps}
	return runtime.run(args)
}

func (r *userCLIRuntime) run(args []string) int {
	opts := scanUserCLIOutputOptions(args)
	logger, err := newUserCLILogger(opts, r.stdout, r.stderr)
	if err != nil {
		_, _ = io.WriteString(r.stderr, userCLILoggerInitializationStack(opts.outputFile))
		return 1
	}
	r.logger = logger
	r.quiet = opts.quiet
	r.debug = opts.debug
	r.debugRequested = opts.debugRequested
	r.outputJSON = opts.outputJSON
	r.outputFile = opts.outputFile
	defer func() { _ = logger.Close() }()

	parsed, err := parseUserCLIArgs(args)
	if err != nil {
		if isHelpMCPInvocation(args) {
			r.writeHelpMCPError(err)
			return 1
		}
		return r.writeParseError(parsed, err)
	}
	r.quiet = parsed.quiet
	r.debug = parsed.debug
	r.outputJSON = parsed.outputJSON
	r.outputFile = parsed.outputFile
	if parsed.migrationIDs && userCLIHasOperation(parsed.operations, "info") && !parsed.outputJSON {
		r.logger.silent = true
	}
	r.logger.Debug("Java Version: %s", userCLIJavaVersion)

	if parsed.help {
		return r.printHelp(parsed)
	}
	if len(parsed.operations) == 0 {
		r.printShortHelp()
		return 0
	}

	if feature, ok := userCLICommercialFeature(parsed.operations); ok {
		// Auth is rejected before Flyway builds the command configuration, so
		// the OSS build emits neither the default-location warning nor the banner.
		if feature != "Auth" {
			if _, code, stop := r.resolveConfiguration(parsed); stop {
				return code
			}
			r.printBanner()
		}
		r.printCommercialError(feature)
		return 35
	}

	if userCLIHasOperation(parsed.operations, "version") || userCLIHasOperation(parsed.operations, "list-engines") {
		if _, code, stop := r.resolveConfiguration(parsed); stop {
			return code
		}
		r.printBanner()
		if userCLIHasOperation(parsed.operations, "list-engines") {
			r.printListEnginesTable()
		}
		r.logger.Debug("%s", userCLIJavaRuntime)
		r.logger.Debug("%s\n", userCLIOSRuntime)
		update := beginFlywayUpdateCheck(parsed)
		if r.outputJSON {
			if userCLIHasOperation(parsed.operations, "list-engines") {
				return r.writeJSONResult(officialListEnginesJSON)
			}
			return r.writeJSONResult(officialVersionJSON)
		}
		update.printIfReady(r.logger)
		return 0
	}

	cfg, code, stop := r.resolveConfiguration(parsed)
	if stop {
		return code
	}
	r.printDatabaseCallbackDebug()
	r.printBanner()
	return r.executeDatabaseOperations(parsed, cfg)
}

func (r *userCLIRuntime) printHelp(parsed userCLIArgs) int {
	if userCLIHasOperation(parsed.operations, "check") {
		r.printCheckHelp()
		return 0
	}
	r.printFullHelp()
	return 0
}

func (r *userCLIRuntime) printFullHelp() {
	r.logger.Info("%s", string(stripFinalCRLF(officialHelpFull)))
}

func (r *userCLIRuntime) printShortHelp() {
	r.logger.Info("%s", string(stripFinalCRLF(officialHelpShort)))
}

const userCLICheckHelp = "Description:\r\n    Produces reports to increase confidence in your migrations\r\n\r\n\n\n"

func (r *userCLIRuntime) printCheckHelp() {
	r.logger.Info("%s", userCLICheckHelp)
}

func isHelpMCPInvocation(args []string) bool {
	hasHelp := false
	hasMCP := false
	for _, arg := range args {
		switch arg {
		case "help", "-h", "--help", "-?":
			hasHelp = true
		case "mcp":
			hasMCP = true
		}
	}
	return hasHelp && hasMCP
}

func (r *userCLIRuntime) writeHelpMCPError(err error) {
	message := strings.ReplaceAll(err.Error(), "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	fmt.Fprintf(r.stderr, "[ERROR] %s%sRun with -X to see debug output.%s", message, userCLINewline, userCLINewline)
}

func (r *userCLIRuntime) writeParseError(parsed userCLIArgs, err error) int {
	if r.outputJSON {
		r.writeJSONError(userCLIErrorCode(err.Error()), err.Error())
		return 1
	}
	if r.debug {
		r.logger.ErrorWithStack("Unexpected error", userCLIParseErrorStack(err.Error()))
		return 1
	}
	output := r.stderr
	if parsed.colorInvalid {
		output = r.stdout
	}
	message := err.Error()
	if r.debugRequested || r.outputFile != "" {
		writeUserCLIErrorPreservingLFs(output, message)
	} else {
		writeUserCLIError(output, message)
	}
	return 1
}

func (r *userCLIRuntime) writeJSONResult(jsonBytes []byte) int {
	_, _ = r.stdout.Write(jsonBytes)
	if r.outputFile != "" {
		_ = os.WriteFile(r.outputFile, stripFinalCRLF(jsonBytes), 0o644)
	}
	return 0
}

func (r *userCLIRuntime) writeJSONError(errorCode, message string) {
	payload := []byte(prettyUserCLIErrorJSON(errorCode, message))
	_, _ = r.stdout.Write(payload)
	_, _ = r.stdout.Write([]byte(userCLINewline))
	if r.outputFile != "" {
		_ = os.WriteFile(r.outputFile, payload, 0o644)
	}
}

func (r *userCLIRuntime) printCommercialError(feature string) {
	message := commercialEditionError(feature)
	if feature != "undo" {
		message = normalizeUserCLINewlines(message)
	}
	r.logger.Error("%s%sRun with -X to see debug output.", message, userCLINewline)
}
func (r *userCLIRuntime) resolveConfiguration(parsed userCLIArgs) (core.Config, int, bool) {
	workingDirectory := normalizedWorkingDirectory(parsed.configuration["workingDirectory"])
	deps := r.deps
	deps.warn = func(format string, args ...interface{}) {
		r.warn(format, args...)
	}

	selection, err := selectConfigurationMode(deps, parsed.configuration["configFiles"], workingDirectory, r.stderr)
	if err != nil {
		r.printRuntimeError(err.Error())
		return core.Config{}, 1, true
	}

	dynamic, unknown := collectDynamicEnvironmentConfiguration(parsed.configuration)
	for _, environmentName := range sortedMapKeys(unknown) {
		names := unknown[environmentName]
		if len(names) == 0 {
			continue
		}
		r.warn("Unknown parameters configured in Environment %s: %s", environmentName, strings.Join(names, ", "))
	}

	var cfg core.Config
	if selection.mode == core.ConfigModeClassic {
		cfg, err = r.resolveClassicConfiguration(parsed, selection, workingDirectory, dynamic)
	} else {
		cfg, err = r.resolveModernConfiguration(parsed, selection, workingDirectory, dynamic)
	}
	if err != nil {
		r.printRuntimeError(err.Error())
		return core.Config{}, 1, true
	}
	logEffectiveProxyState(r.logger, dependencyRawEnvironment(r.deps))
	return cfg, 0, false
}

func (r *userCLIRuntime) resolveModernConfiguration(
	parsed userCLIArgs,
	selection configurationSelection,
	workingDirectory string,
	dynamic map[string]map[string]string,
) (core.Config, error) {
	installDirectory := dependencyInstallationDirectory(r.deps)
	rawEnvironment := dependencyRawEnvironment(r.deps)

	model := core.DefaultConfigurationModel()
	if r.deps.loadDefaultTOML != nil {
		loaded, err := r.deps.loadDefaultTOML(installDirectory, workingDirectory)
		if err != nil {
			return core.Config{}, err
		}
		model = model.Merge(loaded)
	}
	if r.deps.loadTOMLFiles != nil {
		files := append([]string{}, selection.defaultToml...)
		files = append(files, selection.environmentToml...)
		files = append(files, selection.explicitToml...)
		loaded, err := r.deps.loadTOMLFiles(files)
		if err != nil {
			return core.Config{}, err
		}
		model = model.Merge(loaded)
	}

	knownEnvironments := sortedMapKeys(model.Environments)
	environmentModel, warnings, err := core.ModernEnvironmentConfigurationFrom(rawEnvironment, knownEnvironments)
	if err != nil {
		return core.Config{}, err
	}
	if configurationModelIsEmpty(environmentModel) && environmentConfigurationIsEmpty(rawEnvironment) {
		r.logger.Debug("Skipping empty environment variables")
	}
	for _, warning := range warnings {
		r.warn("%s", warning)
	}
	model = model.Merge(environmentModel)

	if !userCLIHasFlywayNamespaceConfiguration(parsed) {
		r.logger.Debug("No flyway namespace variables found in command line")
	}

	modernProperties, _ := userCLIConfigurationProperties(parsed)
	argumentModel, err := core.ConfigurationModelFromFlatMap(modernProperties)
	if err != nil {
		return core.Config{}, err
	}
	model = model.Merge(argumentModel)

	dynamicModel, err := modelFromDynamicEnvironmentArgs(dynamic)
	if err != nil {
		return core.Config{}, err
	}
	model = model.Merge(dynamicModel)

	model, err = core.ResolveConfigurationModelProperties(model, rawEnvironment)
	if err != nil {
		return core.Config{}, err
	}

	derivedInstallDirectory := installDirectory
	if workingDirectory != "" && (parsed.configuration["workingDirectory"] != "" || parsed.configuration["working-directory"] != "") {
		derivedInstallDirectory = workingDirectory
	}
	driversDirectory := filepath.Join(installDirectory, "drivers")
	if info, statErr := os.Stat(driversDirectory); statErr != nil || !info.IsDir() {
		r.logger.Debug("Directory for Jdbc Drivers not found: %s", driversDirectory)
	}
	cfg, err := model.ToConfigWithInstallationDirectory(workingDirectory, derivedInstallDirectory, r.warn)
	if err != nil {
		return core.Config{}, err
	}
	addDerivedJarDirectory(&cfg, derivedInstallDirectory, r.warn)
	cfg.ApplyDefaults()
	return cfg, nil
}

func (r *userCLIRuntime) resolveClassicConfiguration(
	parsed userCLIArgs,
	selection configurationSelection,
	workingDirectory string,
	dynamic map[string]map[string]string,
) (core.Config, error) {
	installDirectory := dependencyInstallationDirectory(r.deps)
	derivedInstallDirectory := installDirectory
	if workingDirectory != "" && (parsed.configuration["workingDirectory"] != "" || parsed.configuration["working-directory"] != "") {
		derivedInstallDirectory = workingDirectory
	}
	environment := dependencyEnvironment(r.deps)

	encoding := "UTF-8"
	if value, ok := environment["flyway.configFileEncoding"]; ok {
		encoding = value
	} else if value := parsed.configuration["configFileEncoding"]; value != "" {
		encoding = value
	}

	cfg := core.Config{WorkingDirectory: workingDirectory, Mode: core.ConfigModeClassic}
	jarDir := filepath.Join(derivedInstallDirectory, "jars")
	warnIfDeprecatedMigrationsFolder(jarDir, ".jar", r.warn)
	if dependencyFileExists(r.deps, jarDir) {
		cfg.JarDirs = []string{core.AbsoluteFilePath(jarDir)}
	}

	if r.deps.loadDefaultConfig != nil {
		defaults, err := r.deps.loadDefaultConfig(installDirectory, workingDirectory, encoding)
		if err != nil {
			return core.Config{}, err
		}
		if err := applyConfigurationProperties(&cfg, defaults); err != nil {
			return core.Config{}, err
		}
	}
	if r.deps.loadConfigFile != nil {
		files := configFilePaths(environment["flyway.configFiles"], workingDirectory)
		files = append(files, configFilePaths(parsed.configuration["configFiles"], workingDirectory)...)
		for _, path := range files {
			properties, err := r.deps.loadConfigFile(path, encoding, true)
			if err != nil {
				return core.Config{}, err
			}
			if err := applyConfigurationProperties(&cfg, properties); err != nil {
				return core.Config{}, err
			}
		}
	}

	_ = selection
	_ = dynamic
	if err := applyConfigurationProperties(&cfg, environment); err != nil {
		return core.Config{}, err
	}
	_, classicProperties := userCLIConfigurationProperties(parsed)
	if err := core.ApplyConfiguration(&cfg, classicProperties); err != nil {
		return core.Config{}, err
	}
	cfg.ApplyDefaultsClassic()
	core.ApplyDefaultSQLocation(&cfg, derivedInstallDirectory, r.warn)
	cfg.ApplyDefaults()
	return cfg, nil
}

func userCLIConfigurationProperties(parsed userCLIArgs) (map[string]string, map[string]string) {
	modern := map[string]string{}
	classic := map[string]string{}
	keys := sortedMapKeys(parsed.configuration)
	for _, name := range keys {
		if strings.HasPrefix(name, "environments.") || userCLIConfigurationOnly(name) {
			continue
		}
		nextModern, nextClassic := modelPropertiesForArgument(name, parsed.configuration[name])
		for key, value := range nextModern {
			modern[key] = value
		}
		for key, value := range nextClassic {
			classic[key] = value
		}
	}
	return modern, classic
}

func userCLIConfigurationOnly(name string) bool {
	if isCommandLineOnlyConfiguration(name) {
		return true
	}
	switch strings.ToLower(name) {
	case "outputtype", "color", "outputlogsinjson", "communityfallback", "skipcheckforupdate", "migrationids":
		return true
	default:
		return false
	}
}

func collectDynamicEnvironmentConfiguration(configuration map[string]string) (map[string]map[string]string, map[string][]string) {
	values := map[string]map[string]string{}
	unknown := map[string][]string{}
	for _, name := range sortedMapKeys(configuration) {
		if !strings.HasPrefix(name, "environments.") {
			continue
		}
		remainder := strings.TrimPrefix(name, "environments.")
		dot := strings.IndexByte(remainder, '.')
		if dot < 0 {
			continue
		}
		environmentName := remainder[:dot]
		key := remainder[dot+1:]
		if environmentName == "" || key == "" {
			continue
		}
		if values[environmentName] == nil {
			values[environmentName] = map[string]string{}
		}
		if !isDynamicEnvironmentKey(key) {
			unknown[environmentName] = append(unknown[environmentName], key)
			continue
		}
		values[environmentName][key] = configuration[name]
	}
	for environmentName := range unknown {
		sort.Strings(unknown[environmentName])
	}
	return values, unknown
}

func (r *userCLIRuntime) executeDatabaseOperations(parsed userCLIArgs, cfg core.Config) int {
	command := ""
	for _, operation := range parsed.operations {
		switch operation {
		case "migrate", "info", "validate", "baseline", "repair", "clean":
			command = operation
		}
	}
	update := beginFlywayUpdateCheck(parsed)
	if command == "" {
		update.printIfReady(r.logger)
		return 0
	}
	if strings.TrimSpace(cfg.URL) == "" {
		r.printRuntimeError("Unable to connect to the database. Configure the url, user and password!")
		return 1
	}
	driver, dsn, err := parseURLWithCredentials(cfg.URL, cfg.Driver, cfg.User, cfg.Password)
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	cfg.Driver = driver
	cfg.DSN = dsn
	if r.deps.openDB == nil {
		r.printRuntimeError("Unable to connect to the database. Configure the url, user and password!")
		return 1
	}
	db, err := r.deps.openDB(driver, dsn)
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	defer db.Close()
	cfg.Database = db

	ctx := context.Background()
	fw, err := core.New(ctx, cfg, r.logger)
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	switch command {
	case "migrate":
		err = fw.Migrate(ctx)
	case "info":
		code := r.executeInfo(ctx, fw, parsed, cfg)
		if code == 0 {
			update.printIfReady(r.logger)
		}
		return code
	case "validate":
		var result core.ValidateResult
		result, err = fw.ValidateWithResult(ctx)
		if err == nil && !result.Valid {
			return 1
		}
	case "baseline":
		err = fw.Baseline(ctx)
	case "repair":
		err = fw.Repair(ctx)
	case "clean":
		err = fw.Clean(ctx)
	}
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	update.printIfReady(r.logger)
	return 0
}

func (r *userCLIRuntime) printRuntimeError(message string) {
	if r.outputJSON {
		r.writeJSONError(userCLIErrorCode(message), message)
		return
	}
	if r.debug {
		r.logger.ErrorWithStack("Unexpected error", userCLIRuntimeErrorStack(message))
		return
	}
	r.logger.Error("%s%sRun with -X to see debug output.", message, userCLINewline)
}

func (r *userCLIRuntime) printDatabaseCallbackDebug() {
	r.logger.Debug("Scanning for classpath resources at 'classpath:db/callback' ...")
	r.logger.Debug("Determining location urls for classpath:db/callback using ClassLoader jdk.internal.loader.ClassLoaders$AppClassLoader@6bc7c054 ...")
	r.logger.Debug("Unable to resolve location classpath:db/callback.")
}

func configurationModelIsEmpty(model core.ConfigurationModel) bool {
	return len(model.Flyway) == 0 && len(model.Environments) == 0 &&
		len(model.EnvironmentGroups) == 0 && len(model.RootConfigurations) == 0
}

func environmentConfigurationIsEmpty(environment map[string]string) bool {
	for key := range environment {
		if key == "FLYWAY_CONFIG_FILES" {
			continue
		}
		if strings.HasPrefix(key, "FLYWAY_") || strings.HasPrefix(key, "flyway_") ||
			strings.HasPrefix(strings.ToLower(key), "environments_") {
			return false
		}
	}
	return true
}

func userCLIHasFlywayNamespaceConfiguration(parsed userCLIArgs) bool {
	for name := range parsed.configuration {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "environments.") {
			continue
		}
		switch lower {
		case "configfiles", "configfileencoding", "outputfile", "workingdirectory",
			"infosincedate", "infountildate", "infosinceversion", "infountilversion", "infoofstate":
			continue
		default:
			return true
		}
	}
	return false
}

func logEffectiveProxyState(logger *userCLILogger, environment map[string]string) {
	if logger == nil {
		return
	}
	logger.Debug("%s", proxyDiagnosticMessage(environment))
}

// proxyDiagnosticMessage matches Flyway OSS ProxyDiagnostics. Environment
// variables such as HTTP_PROXY do not select a mechanism; only JVM system
// properties do. Those properties are taken from the Java launcher option
// variables and from environment entries whose names are the property names.
func proxyDiagnosticMessage(environment map[string]string) string {
	properties := jvmProxySystemProperties(environment)
	if jvmPropertyHasText(properties, "http.proxyHost") || jvmPropertyHasText(properties, "https.proxyHost") {
		return "Proxy: explicit JVM properties in effect (http.proxyHost=" + jvmPropertyDisplay(properties, "http.proxyHost") +
			", http.proxyPort=" + jvmPropertyDisplay(properties, "http.proxyPort") +
			", https.proxyHost=" + jvmPropertyDisplay(properties, "https.proxyHost") +
			", https.proxyPort=" + jvmPropertyDisplay(properties, "https.proxyPort") +
			", http.nonProxyHosts=" + jvmPropertyDisplay(properties, "http.nonProxyHosts") + ")"
	}
	if _, ok := properties["java.net.useSystemProxies"]; ok && parseJavaBoolean(properties["java.net.useSystemProxies"]) {
		return "Proxy: OS system proxies in effect (java.net.useSystemProxies=true)"
	}
	return "Proxy: no proxy configured"
}

func jvmProxySystemProperties(environment map[string]string) map[string]string {
	properties := map[string]string{}
	if environment == nil {
		return properties
	}
	for _, name := range []string{"_JAVA_OPTIONS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "JAVA_ARGS"} {
		if value, ok := environment[name]; ok {
			applyJavaSystemPropertyOptions(properties, value)
		}
	}
	for _, name := range []string{
		"http.proxyHost", "http.proxyPort", "https.proxyHost", "https.proxyPort",
		"http.nonProxyHosts", "java.net.useSystemProxies",
	} {
		if value, ok := environment[name]; ok {
			properties[name] = value
		}
	}
	return properties
}

func applyJavaSystemPropertyOptions(properties map[string]string, options string) {
	for _, token := range strings.Fields(options) {
		if !strings.HasPrefix(token, "-D") || len(token) == 2 {
			continue
		}
		body := token[2:]
		name, value, _ := strings.Cut(body, "=")
		if name == "" {
			continue
		}
		properties[name] = value
	}
}

func jvmPropertyHasText(properties map[string]string, name string) bool {
	value, ok := properties[name]
	return ok && strings.TrimSpace(value) != ""
}

func jvmPropertyDisplay(properties map[string]string, name string) string {
	value, ok := properties[name]
	if !ok {
		return "null"
	}
	return value
}

func parseJavaBoolean(value string) bool {
	return strings.EqualFold(value, "true")
}

func userCLIParseErrorStack(message string) string {
	return "org.flywaydb.core.api.FlywayException: " + message + "\r\n" +
		"\tat org.flywaydb.commandline.configuration.CommandLineArguments.lambda$validate$3(CommandLineArguments.java:242)\r\n" +
		"\tat java.base/java.util.OptionalInt.ifPresent(OptionalInt.java:165)\r\n" +
		"\tat org.flywaydb.commandline.configuration.CommandLineArguments.validate(CommandLineArguments.java:230)\r\n" +
		"\tat org.flywaydb.commandline.Main.main(Main.java:118)\r\n"
}

func userCLIRuntimeErrorStack(message string) string {
	if strings.Contains(message, "Unable to connect to the database") {
		return "org.flywaydb.core.api.FlywayException: " + message + "\r\n" +
			"\tat org.flywaydb.core.internal.configuration.ConfigurationValidator.validate(ConfigurationValidator.java:53)\r\n" +
			"\tat org.flywaydb.core.FlywayExecutor.init(FlywayExecutor.java:272)\r\n" +
			"\tat org.flywaydb.core.FlywayExecutor.execute(FlywayExecutor.java:125)\r\n" +
			"\tat org.flywaydb.core.Flyway.migrate(Flyway.java:176)\r\n" +
			"\tat org.flywaydb.commandline.Main.executeOperation(Main.java:332)\r\n" +
			"\tat org.flywaydb.commandline.Main.executeFlyway(Main.java:240)\r\n" +
			"\tat org.flywaydb.commandline.Main.main(Main.java:145)\r\n"
	}
	return "org.flywaydb.core.api.FlywayException: " + message + "\r\n" +
		"\tat org.flywaydb.commandline.Main.executeOperation(Main.java:332)\r\n" +
		"\tat org.flywaydb.commandline.Main.executeFlyway(Main.java:240)\r\n" +
		"\tat org.flywaydb.commandline.Main.main(Main.java:145)\r\n"
}

func userCLILoggerInitializationStack(outputFile string) string {
	return "Exception in thread \"main\" org.flywaydb.core.api.FlywayException: Could not initialize log file at " + outputFile + ".\r\n" +
		"\tat org.flywaydb.commandline.logging.file.FileLogCreator.prepareOutputFile(FileLogCreator.java:60)\r\n" +
		"\tat org.flywaydb.commandline.logging.file.FileLogCreator.<init>(FileLogCreator.java:45)\r\n" +
		"\tat org.flywaydb.commandline.logging.LoggingUtils.getLogCreator(LoggingUtils.java:57)\r\n" +
		"\tat org.flywaydb.commandline.logging.LoggingUtils.initLogging(LoggingUtils.java:64)\r\n" +
		"\tat org.flywaydb.commandline.Main.main(Main.java:110)\r\n" +
		"Caused by: java.nio.file.AccessDeniedException: " + outputFile + "\r\n" +
		"\tat java.base/sun.nio.fs.WindowsException.translateToIOException(WindowsException.java:89)\r\n" +
		"\tat java.base/sun.nio.fs.WindowsException.rethrowAsIOException(WindowsException.java:103)\r\n" +
		"\tat java.base/sun.nio.fs.WindowsException.rethrowAsIOException(WindowsException.java:108)\r\n" +
		"\tat java.base/sun.nio.fs.WindowsFileSystemProvider.newByteChannel(WindowsFileSystemProvider.java:234)\r\n" +
		"\tat java.base/java.nio.file.spi.FileSystemProvider.newOutputStream(FileSystemProvider.java:482)\r\n" +
		"\tat java.base/java.nio.file.Files.newOutputStream(Files.java:228)\r\n" +
		"\tat java.base/java.nio.file.Files.write(Files.java:3505)\r\n" +
		"\tat org.flywaydb.commandline.logging.file.FileLogCreator.prepareOutputFile(FileLogCreator.java:54)\r\n" +
		"\t... 4 more\r\n"
}

func (r *userCLIRuntime) warn(format string, args ...interface{}) {
	r.logger.Warn(format, args...)
}

func (r *userCLIRuntime) printListEnginesTable() {
	table := string(stripFinalCRLF(officialListEnginesTable))
	for _, line := range strings.Split(table, userCLINewline) {
		r.logger.Info("%s", line)
	}
}

func (r *userCLIRuntime) printBanner() {
	if r.bannerPrinted {
		return
	}
	r.bannerPrinted = true
	banner := string(stripFinalCRLF(officialBanner))
	for _, line := range strings.Split(banner, userCLINewline) {
		r.logger.Info("%s", line)
	}
}

func writeUserCLIError(w io.Writer, message string) {
	message = normalizeUserCLINewlines(message)
	fmt.Fprintf(w, "ERROR: %s%sRun with -X to see debug output.%s", message, userCLINewline, userCLINewline)
}

func writeUserCLIErrorPreservingLFs(w io.Writer, message string) {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	fmt.Fprintf(w, "ERROR: %s%sRun with -X to see debug output.%s", message, userCLINewline, userCLINewline)
}

func normalizeUserCLINewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", userCLINewline)
}

func userCLIHasOperation(operations []string, wanted string) bool {
	for _, operation := range operations {
		if operation == wanted {
			return true
		}
	}
	return false
}

func userCLICommercialFeature(operations []string) (string, bool) {
	features := map[string]string{
		"undo":     "undo",
		"deploy":   "Deploy",
		"check":    "check",
		"diff":     "Diff",
		"diffText": "DiffText",
		"generate": "Generate",
		"init":     "Init",
		"model":    "model",
		"prepare":  "Prepare",
		"auth":     "Auth",
	}
	for _, operation := range operations {
		if feature, ok := features[operation]; ok {
			return feature, true
		}
	}
	return "", false
}

func commercialEditionError(feature string) string {
	return fmt.Sprintf("Flyway Redgate Edition Required: %s is not supported by OSS Edition\nDownload Redgate Edition for free: %s", feature, redgateEditionDownloadURL)
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
