import org.flywaydb.core.Flyway;
import org.flywaydb.core.api.MigrationInfo;
import org.flywaydb.core.api.MigrationVersion;
import org.flywaydb.core.api.configuration.ClassicConfiguration;
import org.flywaydb.core.api.configuration.FluentConfiguration;
import org.flywaydb.core.api.output.CommandResultFactory;
import org.flywaydb.core.internal.configuration.ConfigUtils;
import org.flywaydb.core.internal.configuration.models.ConfigurationModel;
import org.flywaydb.core.internal.configuration.models.EnvironmentModel;
import org.flywaydb.core.internal.configuration.models.FlywayEnvironmentModel;
import org.flywaydb.core.utilities.configuration.TomlUtils;

import java.io.File;
import java.nio.file.Path;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/**
 * Canonical Flyway oracle used by run-parity.ps1.
 *
 * Usage:
 *   ParityRunner <action> <jdbcUrl> <user> <password> <location> [key=value...]
 *
 * Canonical actions: migrate, info, validate, repair, baseline, clean, undo.
 * Timestamp values are emitted in both official forms. The main parity driver
 * normalizes those two columns because Java and Go execute in separate fresh
 * databases; run-installed-on.ps1 compares them exactly against one shared row.
 */
public final class ParityRunner {
    private ParityRunner() {}

    public static void main(String[] args) {
        if (args.length < 5) {
            System.err.println("usage: ParityRunner <action> <jdbcUrl> <user> <password> <location> [key=value...]");
            System.exit(2);
        }

        String action = args[0].toLowerCase(Locale.ROOT);
        String url = args[1];
        String user = args[2];
        String password = args[3];
        String locations = args[4];
        Map<String, String> options = new HashMap<>();
        for (int i = 5; i < args.length; i++) {
            int equals = args[i].indexOf('=');
            if (equals > 0) {
                options.put(args[i].substring(0, equals).toLowerCase(Locale.ROOT), args[i].substring(equals + 1));
            }
        }

        try {
            Flyway flyway;
            String configMode = option(options, "configmode");
            if ("classic".equalsIgnoreCase(configMode)) {
                flyway = loadClassic(options, url, user, password, locations);
            } else if ("modern".equalsIgnoreCase(configMode)) {
                flyway = loadModern(options);
            } else if ("auto".equalsIgnoreCase(configMode)) {
                flyway = loadAuto(options, url, user, password, locations);
            } else {
            FluentConfiguration config = Flyway.configure()
                    .dataSource(url, user, password)
                    .locations(locationArgs(locations));

            Map<String, String> placeholders = new HashMap<>();
            for (Map.Entry<String, String> entry : options.entrySet()) {
                String key = entry.getKey();
                if (key.startsWith("placeholders.")) {
                    placeholders.put(key.substring("placeholders.".length()), entry.getValue());
                }
                else if (key.equals("placeholders")) {
                    for (String item : splitCsv(entry.getValue())) {
                        int equals = item.indexOf('=');
                        if (equals > 0) placeholders.put(item.substring(0, equals), item.substring(equals + 1));
                    }
                }
            }
            if (!placeholders.isEmpty()) config.placeholders(placeholders);

            String callbackLocations = option(options, "callbacklocations");
            if (callbackLocations != null) config.callbackLocations(locationArgs(callbackLocations));

            String target = option(options, "target");
            if (target != null) {
                // ClassicConfiguration.getTarget() implements the official optional-target
                // syntax: a trailing "?" strips the marker and disables failOnMissingTarget.
                if (target.endsWith("?")) {
                    config.configuration(Map.of("flyway.target", target));
                } else {
                    config.target(MigrationVersion.fromVersion(target));
                }
            }
            setBoolean(options, "validateonmigrate", config::validateOnMigrate);
            setBoolean(options, "baselineonmigrate", config::baselineOnMigrate);
            setBoolean(options, "cleandisabled", config::cleanDisabled);
            setBoolean(options, "outoforder", config::outOfOrder);
            setBoolean(options, "validatemigrationnaming", config::validateMigrationNaming);
            setBoolean(options, "executeintransaction", config::executeInTransaction);
            setBoolean(options, "placeholderreplacement", config::placeholderReplacement);
            setBoolean(options, "mixed", config::mixed);
            setBoolean(options, "group", config::group);
            setBoolean(options, "createschemas", config::createSchemas);

            String baselineVersion = option(options, "baselineversion");
            if (baselineVersion != null) config.baselineVersion(baselineVersion);
            String baselineDescription = option(options, "baselinedescription");
            if (baselineDescription != null) config.baselineDescription(baselineDescription);

            String ignorePatterns = option(options, "ignoremigrationpatterns");
            if (ignorePatterns != null && !ignorePatterns.isBlank()) {
                config.ignoreMigrationPatterns(splitCsv(ignorePatterns));
            }

            String schemas = option(options, "schemas");
            if (schemas != null && !schemas.isBlank()) {
                config.schemas(splitCsv(schemas));
            }

            String placeholderPrefix = option(options, "placeholderprefix");
            if (placeholderPrefix != null) config.placeholderPrefix(placeholderPrefix);
            String placeholderSuffix = option(options, "placeholdersuffix");
            if (placeholderSuffix != null) config.placeholderSuffix(placeholderSuffix);

            // PostgreSQL-specific setting is exposed through the generic
            // configuration map in Flyway 13.13.
            Map<String, String> generic = new HashMap<>();
            String pgLock = option(options, "postgresql.transactional.lock");
            if (pgLock == null) pgLock = option(options, "pgtransactionallock");
            if (pgLock != null) generic.put("flyway.postgresql.transactional.lock", pgLock);

            String baselineMigrationPrefix = option(options, "baselinemigrationprefix");
            if (baselineMigrationPrefix != null) {
                generic.put("flyway.baselineMigrationPrefix", baselineMigrationPrefix);
            }

            if (!generic.isEmpty()) config.configuration(generic);

            flyway = config.load();
            }
            switch (action) {
                case "migrate" -> {
                    var result = flyway.migrate();
                    System.out.println("RESULT\tmigrate\tsuccess=" + result.success);
                }
                case "info" -> {
                    var result = flyway.info();
                    MigrationInfo[] all = result.all();
                    System.out.println("RESULT\tinfo\tsuccess=true\tcount=" + all.length);
                    for (MigrationInfo info : all) {
                        System.out.println(infoLine(info));
                    }
                }
                case "validate" -> {
                    var result = flyway.validateWithResult();
                    System.out.println("RESULT\tvalidate\tvalid=" + result.validationSuccessful
                            + "\tinvalidMigrations=" + result.invalidMigrations.size()
                            + "\tvalidateCount=" + result.validateCount);
                    for (var invalid : result.invalidMigrations) {
                        System.out.println("INVALID\t" + sanitize(String.valueOf(invalid.version))
                                + "\t" + sanitize(invalid.description)
                                + "\t" + sanitize(invalid.filepath)
                                + "\t" + sanitize(String.valueOf(invalid.errorDetails.errorCode))
                                + "\t" + sanitize(invalid.errorDetails.errorMessage));
                    }
                    // Mirror the Flyway CLI: a failed validation exits non-zero,
                    // while validateWithResult itself does not throw.
                    if (!result.validationSuccessful) {
                        System.exit(1);
                    }
                }
                case "repair" -> {
                    flyway.repair();
                    System.out.println("RESULT\trepair\tsuccess=true");
                }
                case "baseline" -> {
                    var result = flyway.baseline();
                    System.out.println("RESULT\tbaseline\tsuccess=" + result.successfullyBaselined
                            + "\tbaselineVersion=" + sanitize(String.valueOf(result.baselineVersion)));
                }
                case "clean" -> {
                    flyway.clean();
                    System.out.println("RESULT\tclean\tsuccess=true");
                }
                case "undo" -> {
                    flyway.undo();
                    System.out.println("RESULT\tundo\tsuccess=true");
                }
                default -> throw new IllegalArgumentException("unknown action: " + action);
            }
        } catch (Throwable t) {
            String message = t.getMessage();
            if (message == null || message.isBlank()) message = t.getClass().getSimpleName();
            int newline = message.indexOf('\n');
            if (newline >= 0) message = message.substring(0, newline);
            System.out.println("ERROR\t" + t.getClass().getName() + "\t" + sanitize(message));
            System.exit(1);
        }
    }

    private static Flyway loadAuto(Map<String, String> options, String url, String user,
                                   String password, String locations) {
        String workingDirectory = option(options, "workingdirectory");
        String installationDirectory = option(options, "installationdirectory");
        if (installationDirectory == null) installationDirectory = ".";

        List<File> cliFiles = existingConfigFiles(options, workingDirectory);
        List<File> cliToml = filterToml(cliFiles, true);
        List<File> cliConf = filterToml(cliFiles, false);
        Boolean cliChoice = chooseConfigMode(cliToml, cliConf, true);
        if (cliChoice != null) {
            return cliChoice ? loadModern(options) : loadClassic(options, url, user, password, locations);
        }

        List<File> envToml = configFilesFromEnvironment(workingDirectory, true);
        List<File> envConf = configFilesFromEnvironment(workingDirectory, false);
        Boolean envChoice = chooseConfigMode(envToml, envConf, true);
        if (envChoice != null) {
            return envChoice ? loadModern(options) : loadClassic(options, url, user, password, locations);
        }

        List<File> defaultToml = new ArrayList<>();
        for (File file : ConfigUtils.getDefaultTomlConfigFileLocations(
                new File(installationDirectory), workingDirectory)) {
            if (file.exists()) defaultToml.add(file);
        }
        List<File> defaultConf = new ArrayList<>();
        for (String path : defaultLegacyConfigurationPaths(installationDirectory, workingDirectory)) {
            File file = new File(path);
            if (file.exists()) defaultConf.add(file);
        }
        Boolean defaultChoice = chooseConfigMode(defaultToml, defaultConf, false);
        if (defaultChoice == null) defaultChoice = true;
        return defaultChoice ? loadModern(options) : loadClassic(options, url, user, password, locations);
    }

    private static Boolean chooseConfigMode(List<File> tomlFiles, List<File> confFiles,
                                            boolean throwOnConflict) {
        if (!tomlFiles.isEmpty()) {
            if (!confFiles.isEmpty() && throwOnConflict) {
                throw new org.flywaydb.core.api.FlywayException(
                        "Using both TOML configuration and CONF configuration is not supported. Please remove the CONF configuration files.\n"
                        + "TOML files: " + joinPaths(tomlFiles) + System.lineSeparator()
                        + "CONF files: " + joinPaths(confFiles) + System.lineSeparator());
            }
            return true;
        }
        if (!confFiles.isEmpty()) return false;
        return null;
    }

    private static List<File> existingConfigFiles(Map<String, String> options, String workingDirectory) {
        List<File> result = new ArrayList<>();
        String raw = option(options, "configfiles");
        if (raw == null) return result;
        for (String item : splitCsv(raw)) {
            File file = resolveConfigFile(item, workingDirectory);
            if (file.exists()) result.add(file);
        }
        return result;
    }

    private static List<File> configFilesFromEnvironment(String workingDirectory, boolean toml) {
        List<File> result = new ArrayList<>();
        String raw = System.getenv("FLYWAY_CONFIG_FILES");
        if (raw == null) return result;
        for (String item : splitCsv(raw)) {
            if (item.endsWith(".toml") != toml) continue;
            result.add(resolveConfigFile(item, workingDirectory));
        }
        return result;
    }

    private static List<File> filterToml(List<File> files, boolean toml) {
        List<File> result = new ArrayList<>();
        for (File file : files) {
            if (file.getName().endsWith(".toml") == toml) result.add(file);
        }
        return result;
    }

    private static List<String> defaultLegacyConfigurationPaths(String installationDirectory,
                                                                String workingDirectory) {
        List<String> files = new ArrayList<>();
        files.add(new File(new File(installationDirectory), "conf/flyway.conf").getAbsolutePath());
        files.add(new File(System.getProperty("user.home"), "flyway.conf").getAbsolutePath());
        files.add(new File(System.getProperty("user.dir"), "flyway.conf").getAbsolutePath());
        if (workingDirectory != null && !workingDirectory.isBlank()) {
            files.add(new File(workingDirectory, "flyway.conf").getAbsolutePath());
        }
        return files;
    }

    private static String joinPaths(List<File> files) {
        List<String> paths = new ArrayList<>();
        for (File file : files) paths.add(file.getAbsolutePath());
        return String.join(", ", paths);
    }

    private static Flyway loadModern(Map<String, String> options) {
        String workingDirectory = option(options, "workingdirectory");
        String installationDirectory = option(options, "installationdirectory");
        if (installationDirectory == null) installationDirectory = ".";

        List<File> files = new ArrayList<>();
        files.addAll(ConfigUtils.getDefaultTomlConfigFileLocations(
                new File(installationDirectory), workingDirectory));
        files.addAll(configFilesFromEnvironment(workingDirectory, true));
        String rawConfigFiles = option(options, "configfiles");
        if (rawConfigFiles != null) {
            for (String item : splitCsv(rawConfigFiles)) {
                if (item.endsWith(".toml")) {
                    files.add(resolveConfigFile(item, workingDirectory));
                }
            }
        }
        List<File> existing = new ArrayList<>();
        for (File file : files) {
            if (file.exists()) existing.add(file);
        }

        ConfigurationModel config = ConfigurationModel.defaults()
                .merge(TomlUtils.loadConfigurationFiles(existing));
        ConfigurationModel environmentModel = TomlUtils.loadConfigurationFromEnvironment(
                config.getEnvironments().keySet());
        ConfigurationModel commandLineModel = TomlUtils.loadConfigurationFromCommandlineArgs(
                modernCommandLineConfiguration(options));
        config = config.merge(environmentModel);
        config = config.merge(commandLineModel);

        String currentEnvironment = config.getFlyway().getEnvironment();
        if (currentEnvironment == null || currentEnvironment.isBlank()) currentEnvironment = "default";
        EnvironmentModel envTemp = environmentModel.getEnvironments().get("tempConfigEnvironment");
        if (envTemp != null) {
            EnvironmentModel existingEnv = config.getEnvironments().get(currentEnvironment);
            config.getEnvironments().put(currentEnvironment,
                    existingEnv == null ? envTemp : existingEnv.merge(envTemp));
        }
        EnvironmentModel cliTemp = commandLineModel.getEnvironments().get("tempConfigEnvironment");
        if (cliTemp != null) {
            EnvironmentModel existingEnv = config.getEnvironments().get(currentEnvironment);
            config.getEnvironments().put(currentEnvironment,
                    existingEnv == null ? cliTemp : existingEnv.merge(cliTemp));
        }
        config.getEnvironments().remove("tempConfigEnvironment");

        Map<String, String> named = new HashMap<>();
        for (Map.Entry<String, String> entry : options.entrySet()) {
            if (entry.getKey().startsWith("environments.")) {
                named.put(restoreModernNames(entry.getKey()), entry.getValue());
            }
        }
        if (!named.isEmpty()) {
            config = config.merge(TomlUtils.loadConfigurationFromCommandlineArgs(named));
        }

        ClassicConfiguration cfg = new ClassicConfiguration(config);
        cfg.setWorkingDirectory(workingDirectory);
        if (workingDirectory != null) {
            ConfigUtils.makeRelativeLocationsBasedOnWorkingDirectory(
                    workingDirectory, config.getFlyway().getLocations());
            ConfigUtils.makeRelativeLocationsBasedOnWorkingDirectory(
                    workingDirectory, config.getFlyway().getCallbackLocations());
            ConfigUtils.makeRelativeLocationsInEnvironmentsBasedOnWorkingDirectory(
                    workingDirectory, config.getEnvironments());
            ConfigUtils.makeRelativeJarDirsBasedOnWorkingDirectory(
                    workingDirectory, config.getFlyway().getJarDirs());
            ConfigUtils.makeRelativeJarDirsInEnvironmentsBasedOnWorkingDirectory(
                    workingDirectory, config.getEnvironments());
        }
        return new Flyway(cfg);
    }

    private static Map<String, String> modernCommandLineConfiguration(Map<String, String> options) {
        Map<String, String> result = new HashMap<>();
        Set<String> environmentFields = Set.of(
                "url", "user", "password", "driver", "displayName", "schemas",
                "connectRetries", "connectRetriesInterval", "initSql", "jdbcProperties",
                "resolvers", "provisioner", "environmentGroups");
        for (Map.Entry<String, String> entry : options.entrySet()) {
            String key = entry.getKey();
            if (key.equals("configmode") || key.equals("configfiles")
                    || key.equals("configfileencoding") || key.equals("workingdirectory")
                    || key.equals("installationdirectory") || key.startsWith("environments.")) {
                continue;
            }
            if (key.equals("placeholders")) {
                for (String item : splitCsv(entry.getValue())) {
                    int equals = item.indexOf('=');
                    if (equals > 0) {
                        result.put("flyway.placeholders." + item.substring(0, equals),
                                item.substring(equals + 1));
                    }
                }
                continue;
            }
            if (key.startsWith("placeholders.")) {
                result.put("flyway." + restoreModernNames(key), entry.getValue());
                continue;
            }
            String restored = restoreModernNames(key);
            String top = restored.contains(".") ? restored.substring(0, restored.indexOf('.')) : restored;
            if (environmentFields.contains(top)) {
                result.put("environments.tempConfigEnvironment." + restored, entry.getValue());
            } else if (restored.startsWith("postgresql.") || restored.equals("pgtransactionallock")) {
                result.put("flyway.postgresql.transactional.lock", entry.getValue());
            } else {
                result.put("flyway." + restored, entry.getValue());
            }
        }
        return result;
    }

    private static String restoreModernNames(String lower) {
        String[][] names = {
                {"baselineversion", "baselineVersion"},
                {"baselinedescription", "baselineDescription"},
                {"baselinemigrationprefix", "baselineMigrationPrefix"},
                {"baselineonmigrate", "baselineOnMigrate"},
                {"callbacklocations", "callbackLocations"},
                {"cleandisabled", "cleanDisabled"},
                {"connectretriesinterval", "connectRetriesInterval"},
                {"connectretries", "connectRetries"},
                {"createschemas", "createSchemas"},
                {"defaultschema", "defaultSchema"},
                {"detectencoding", "detectEncoding"},
                {"displayname", "displayName"},
                {"environmentgroups", "environmentGroups"},
                {"executeintransaction", "executeInTransaction"},
                {"failonmissinglocations", "failOnMissingLocations"},
                {"ignoremigrationpatterns", "ignoreMigrationPatterns"},
                {"initsql", "initSql"},
                {"installedby", "installedBy"},
                {"jdbcproperties", "jdbcProperties"},
                {"lockretrycount", "lockRetryCount"},
                {"outoforder", "outOfOrder"},
                {"placeholderprefix", "placeholderPrefix"},
                {"placeholderreplacement", "placeholderReplacement"},
                {"placeholderseparator", "placeholderSeparator"},
                {"placeholdersuffix", "placeholderSuffix"},
                {"repeatablesqlmigrationprefix", "repeatableSqlMigrationPrefix"},
                {"scriptplaceholderprefix", "scriptPlaceholderPrefix"},
                {"scriptplaceholdersuffix", "scriptPlaceholderSuffix"},
                {"skipdefaultcallbacks", "skipDefaultCallbacks"},
                {"skipdefaultresolvers", "skipDefaultResolvers"},
                {"sqlmigrationprefix", "sqlMigrationPrefix"},
                {"sqlmigrationseparator", "sqlMigrationSeparator"},
                {"sqlmigrationsuffixes", "sqlMigrationSuffixes"},
                {"validateonmigrate", "validateOnMigrate"},
                {"validatemigrationnaming", "validateMigrationNaming"},
                {"pgtransactionallock", "postgresql.transactional.lock"}
        };
        for (String[] name : names) {
            if (lower.equals(name[0])) return name[1];
            if (lower.startsWith(name[0] + ".")) {
                return name[1] + lower.substring(name[0].length());
            }
        }
        if (lower.startsWith("environments.")) {
            int secondDot = lower.indexOf('.', "environments.".length());
            if (secondDot > 0) {
                String prefix = lower.substring(0, secondDot + 1);
                return prefix + restoreModernNames(lower.substring(secondDot + 1));
            }
        }
        return lower;
    }
    private static Flyway loadClassic(Map<String, String> options, String url, String user,
                                      String password, String locations) {
        String workingDirectory = option(options, "workingdirectory");
        if (workingDirectory == null) workingDirectory = "";
        String installationDirectory = option(options, "installationdirectory");
        if (installationDirectory == null) installationDirectory = ".";
        Map<String, String> env = ConfigUtils.environmentVariablesToPropertyMap();
        String encoding = env.getOrDefault("flyway.configFileEncoding",
                option(options, "configfileencoding"));
        if (encoding == null || encoding.isBlank()) encoding = "UTF-8";

        ClassicConfiguration config = new ClassicConfiguration();
        config.setWorkingDirectory(workingDirectory);

        // Reproduce LegacyConfigurationManager config-file bootstrap. This
        // must happen before the generic environment/CLI overlays, because the
        // extra files are themselves discovered from env and CLI options.
        Map<String, String> fileProperties = ConfigUtils.loadDefaultConfigurationFiles(
                new File(installationDirectory), workingDirectory, encoding);
        for (File file : legacyConfigFilesFromEnvironment(workingDirectory)) {
            fileProperties.putAll(ConfigUtils.loadConfigurationFile(file, encoding, true));
        }
        for (File file : legacyConfigFilesFromOptions(options, workingDirectory)) {
            if (!file.getName().endsWith(".toml")) {
                fileProperties.putAll(ConfigUtils.loadConfigurationFile(file, encoding, true));
            }
        }
        config.configure(fileProperties);
        config.configureUsingEnvVars();
        config.configure(explicitConfiguration(options));
        return new Flyway(config);
    }

    private static List<File> legacyConfigFilesFromEnvironment(String workingDirectory) {
        List<File> files = new ArrayList<>();
        String raw = System.getenv("FLYWAY_CONFIG_FILES");
        if (raw == null) return files;
        for (String item : splitCsv(raw)) {
            if (item.endsWith(".toml")) continue;
            files.add(resolveConfigFile(item, workingDirectory));
        }
        return files;
    }

    private static List<File> legacyConfigFilesFromOptions(Map<String, String> options,
                                                           String workingDirectory) {
        List<File> files = new ArrayList<>();
        String raw = option(options, "configfiles");
        if (raw == null) return files;
        for (String item : splitCsv(raw)) {
            files.add(resolveConfigFile(item, workingDirectory));
        }
        return files;
    }

    private static File resolveConfigFile(String value, String workingDirectory) {
        Path path = Path.of(value);
        if (!path.isAbsolute() && workingDirectory != null && !workingDirectory.isBlank()) {
            path = Path.of(workingDirectory).resolve(path);
        }
        return path.toAbsolutePath().normalize().toFile();
    }

    private static Map<String, String> explicitConfiguration(Map<String, String> options) {
        Map<String, String> properties = new HashMap<>();
        putOption(options, properties, "url", "flyway.url");
        putOption(options, properties, "user", "flyway.user");
        putOption(options, properties, "password", "flyway.password");
        putOption(options, properties, "driver", "flyway.driver");
        putOption(options, properties, "locations", "flyway.locations");
        putOption(options, properties, "callbacklocations", "flyway.callbackLocations");
        putOption(options, properties, "table", "flyway.table");
        putOption(options, properties, "schemas", "flyway.schemas");
        putOption(options, properties, "defaultschema", "flyway.defaultSchema");
        putOption(options, properties, "baselineversion", "flyway.baselineVersion");
        putOption(options, properties, "baselinedescription", "flyway.baselineDescription");
        putOption(options, properties, "baselinemigrationprefix", "flyway.baselineMigrationPrefix");
        putOption(options, properties, "target", "flyway.target");
        putOption(options, properties, "ignoremigrationpatterns", "flyway.ignoreMigrationPatterns");
        putOption(options, properties, "placeholderprefix", "flyway.placeholderPrefix");
        putOption(options, properties, "placeholdersuffix", "flyway.placeholderSuffix");
        putOption(options, properties, "placeholderseparator", "flyway.placeholderSeparator");
        putOption(options, properties, "encoding", "flyway.encoding");
        putOption(options, properties, "initsql", "flyway.initSql");
        putOption(options, properties, "installedby", "flyway.installedBy");
        putOption(options, properties, "postgresql.transactional.lock", "flyway.postgresql.transactional.lock");
        putOption(options, properties, "pgtransactionallock", "flyway.postgresql.transactional.lock");

        putOption(options, properties, "validateonmigrate", "flyway.validateOnMigrate");
        putOption(options, properties, "baselineonmigrate", "flyway.baselineOnMigrate");
        putOption(options, properties, "cleandisabled", "flyway.cleanDisabled");
        putOption(options, properties, "outoforder", "flyway.outOfOrder");
        putOption(options, properties, "validatemigrationnaming", "flyway.validateMigrationNaming");
        putOption(options, properties, "executeintransaction", "flyway.executeInTransaction");
        putOption(options, properties, "placeholderreplacement", "flyway.placeholderReplacement");
        putOption(options, properties, "mixed", "flyway.mixed");
        putOption(options, properties, "group", "flyway.group");
        putOption(options, properties, "createschemas", "flyway.createSchemas");
        putOption(options, properties, "failonmissinglocations", "flyway.failOnMissingLocations");
        putOption(options, properties, "skipdefaultcallbacks", "flyway.skipDefaultCallbacks");

        String placeholders = option(options, "placeholders");
        if (placeholders != null) {
            for (String item : splitCsv(placeholders)) {
                int equals = item.indexOf('=');
                if (equals > 0) properties.put("flyway.placeholders." + item.substring(0, equals), item.substring(equals + 1));
            }
        }
        for (Map.Entry<String, String> entry : options.entrySet()) {
            if (entry.getKey().startsWith("placeholders.")) {
                properties.put("flyway.placeholders." + entry.getKey().substring("placeholders.".length()), entry.getValue());
            }
        }
        return properties;
    }

    private static void putOption(Map<String, String> options, Map<String, String> properties,
                                  String optionName, String propertyName) {
        String value = option(options, optionName);
        if (value != null) properties.put(propertyName, value);
    }
    private static String infoLine(MigrationInfo info) {
        String typeName = String.valueOf(info.getType());
        String category;
        if (info.getType().isSynthetic()) {
            category = "";
        } else if (info.getVersion() == null) {
            category = "Repeatable";
        } else if (info.getType().isUndo()) {
            category = "Undo";
        } else if (info.getType().isBaseline()) {
            category = "Baseline";
        } else {
            category = "Versioned";
        }
        String version = info.getVersion() == null ? "NULL" : sanitize(info.getVersion().toString());
        String checksum = info.getChecksum() == null ? "NULL" : info.getChecksum().toString();
        String rank = info.getInstalledRank() == null ? "NULL" : info.getInstalledRank().toString();
        String installedBy = info.getInstalledBy() == null || info.getInstalledBy().isBlank()
                ? "NULL" : sanitize(info.getInstalledBy());
        String installedOn = info.getInstalledOn() == null
                ? "NULL" : sanitize(info.getInstalledOn().toString());
        var output = CommandResultFactory.createInfoOutput(Set.of(), info);
        String installedOnUTC = output.getInstalledOnUTC() == null || output.getInstalledOnUTC().isBlank()
                ? "NULL" : sanitize(output.getInstalledOnUTC());
        return "INFO\t" + category
                + "\t" + version
                + "\t" + sanitize(info.getDescription())
                + "\t" + sanitize(typeName)
                + "\t" + sanitize(String.valueOf(info.getState()))
                + "\t" + sanitize(String.valueOf(info.getScript()))
                + "\t" + checksum
                + "\t" + rank
                + "\t" + installedBy
                + "\t" + installedOn
                + "\t" + installedOnUTC;
    }

    private static String[] locationArgs(String locations) {
        List<String> result = new ArrayList<>();
        for (String location : splitCsv(locations)) {
            result.add(location.startsWith("filesystem:") ? location : "filesystem:" + location);
        }
        return result.toArray(String[]::new);
    }

    private static String[] splitCsv(String value) {
        List<String> result = new ArrayList<>();
        for (String item : value.split(",")) {
            String trimmed = item.trim();
            if (!trimmed.isEmpty()) result.add(trimmed);
        }
        return result.toArray(String[]::new);
    }

    private static String option(Map<String, String> options, String key) {
        return options.get(key.toLowerCase(Locale.ROOT));
    }

    private static void setBoolean(Map<String, String> options, String key, BooleanSetter setter) {
        String value = option(options, key);
        if (value != null) setter.set(Boolean.parseBoolean(value));
    }

    private static String sanitize(String value) {
        if (value == null) return "NULL";
        return value.replace('\r', ' ').replace('\n', ' ').replace('\t', ' ').trim();
    }

    @FunctionalInterface
    private interface BooleanSetter {
        void set(boolean value);
    }
}
