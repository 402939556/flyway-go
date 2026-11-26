import org.flywaydb.core.Flyway;
import org.flywaydb.core.api.configuration.FluentConfiguration;
import org.flywaydb.core.api.MigrationVersion;
import java.util.Map;

public final class AdvancedRunner {
    private static String opt(String[] args, int i) {
        return i < args.length && !args[i].isEmpty() ? args[i] : null;
    }

    public static void main(String[] args) {
        if (args.length < 5) {
            System.err.println("usage: AdvancedRunner <migrate|info|validate|repair|clean> <jdbcUrl> <user> <password> <location> [callbackLocation] [placeholderKey] [placeholderValue] [target] [ignoreMigrationPatterns] [cleanDisabled]");
            System.exit(2);
        }
        String action = args[0];
        String url = args[1];
        String user = args[2];
        String pass = args[3];
        String location = args[4];
        String callbackLocation = opt(args, 5);
        String placeholderKey = opt(args, 6);
        String placeholderValue = opt(args, 7);
        String target = opt(args, 8);
        String ignoreMigrationPatterns = opt(args, 9);
        String cleanDisabled = opt(args, 10);
        try {
            FluentConfiguration config = Flyway.configure()
                    .dataSource(url, user, pass)
                    .locations("filesystem:" + location);
            if (callbackLocation != null) config.callbackLocations("filesystem:" + callbackLocation);
            if (placeholderKey != null) config.placeholders(Map.of(placeholderKey, placeholderValue == null ? "" : placeholderValue));
            if (target != null) config.target(MigrationVersion.fromVersion(target));
            if (ignoreMigrationPatterns != null) config.ignoreMigrationPatterns(ignoreMigrationPatterns.split(","));
            if (cleanDisabled != null) config.cleanDisabled(Boolean.parseBoolean(cleanDisabled));
            Flyway flyway = config.load();
            switch (action) {
                case "migrate" -> {
                    var result = flyway.migrate();
                    System.out.println("RESULT\tmigrate\tsuccess=" + result.success + "\tmigrationsExecuted=" + result.migrationsExecuted);
                }
                case "info" -> {
                    var result = flyway.info();
                    for (var info : result.all()) {
                        System.out.println("INFO\t" + (info.isRepeatable() ? "Repeatable" : "Versioned") + "\t" + info.getVersion() + "\t" + info.getDescription() + "\t" + info.getType() + "\t" + info.getState() + "\t" + info.getScript());
                    }
                }
                case "validate" -> {
                    var result = flyway.validateWithResult();
                    System.out.println("RESULT\tvalidate\tvalid=" + result.validationSuccessful + "\tinvalidMigrations=" + result.invalidMigrations.size() + "\tvalidateCount=" + result.validateCount);
                    for (var invalid : result.invalidMigrations) {
                        System.out.println("INVALID\t" + invalid.version + "\t" + invalid.description + "\t" + invalid.filepath + "\t" + invalid.errorDetails.errorCode + "\t" + invalid.errorDetails.errorMessage);
                    }
                }
                case "repair" -> {
                    var result = flyway.repair();
                    System.out.println("RESULT\trepair\trepairActions=" + result.repairActions.size() + "\talignedMigrations=" + result.migrationsAligned.size());
                    for (var repairAction : result.repairActions) System.out.println("REPAIR\t" + repairAction);
                }
                case "clean" -> {
                    flyway.clean();
                    System.out.println("RESULT\tclean\tsuccess=true");
                }
                default -> throw new IllegalArgumentException("unknown action: " + action);
            }
        } catch (Throwable t) {
            System.out.println("ERROR\t" + t.getClass().getName() + "\t" + t.getMessage());
            t.printStackTrace(System.out);
            System.exit(1);
        }
    }
}



