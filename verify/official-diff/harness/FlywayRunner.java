import org.flywaydb.core.Flyway;
import org.flywaydb.core.api.MigrationInfo;
import org.flywaydb.core.api.MigrationVersion;

public final class FlywayRunner {
    private static String opt(String[] args, int i) {
        return i < args.length && !args[i].isEmpty() ? args[i] : null;
    }

    public static void main(String[] args) {
        if (args.length < 5) {
            System.err.println("usage: FlywayRunner <migrate|info|validate|clean|baseline|repair> <jdbcUrl> <user> <password> <location> [target] [validateOnMigrate] [baselineOnMigrate] [cleanDisabled] [pgTransactionalLock] [outOfOrder] [ignoreMigrationPatterns] [baselineVersion] [baselineDescription] [validateMigrationNaming]");
            System.exit(2);
        }
        String action = args[0];
        String url = args[1];
        String user = args[2];
        String pass = args[3];
        String location = args[4];
        String target = opt(args, 5);
        String validateOnMigrate = opt(args, 6);
        String baselineOnMigrate = opt(args, 7);
        String cleanDisabled = opt(args, 8);
        String pgTransactionalLock = opt(args, 9);
        String outOfOrder = opt(args, 10);
        String ignoreMigrationPatterns = opt(args, 11);
        String baselineVersion = opt(args, 12);
        String baselineDescription = opt(args, 13);
        String validateMigrationNaming = opt(args, 14);
        try {
            var config = Flyway.configure()
                    .dataSource(url, user, pass)
                    .locations("filesystem:" + location);
            if (target != null) config.target(MigrationVersion.fromVersion(target));
            if (validateOnMigrate != null) config.validateOnMigrate(Boolean.parseBoolean(validateOnMigrate));
            if (baselineOnMigrate != null) config.baselineOnMigrate(Boolean.parseBoolean(baselineOnMigrate));
            if (cleanDisabled != null) config.cleanDisabled(Boolean.parseBoolean(cleanDisabled));
            if (pgTransactionalLock != null) config.configuration(java.util.Map.of("flyway.postgresql.transactional.lock", pgTransactionalLock));
            if (outOfOrder != null) config.outOfOrder(Boolean.parseBoolean(outOfOrder));
            if (ignoreMigrationPatterns != null) config.ignoreMigrationPatterns(ignoreMigrationPatterns.split(","));
            if (baselineVersion != null) config.baselineVersion(baselineVersion);
            if (baselineDescription != null) config.baselineDescription(baselineDescription);
            if (validateMigrationNaming != null) config.validateMigrationNaming(Boolean.parseBoolean(validateMigrationNaming));
            Flyway flyway = config.load();
            switch (action) {
                case "migrate" -> {
                    var result = flyway.migrate();
                    System.out.println("RESULT\tmigrate\tsuccess=" + result.success + "\tmigrationsExecuted=" + result.migrationsExecuted);
                }
                case "info" -> {
                    var result = flyway.info();
                    for (MigrationInfo info : result.all()) {
                        System.out.println("INFO\t" + (info.isRepeatable() ? "Repeatable" : "Versioned") + "\t" + info.getVersion() + "\t" + info.getDescription() + "\t" + info.getType() + "\t" + info.getState() + "\t" + info.getInstalledOn() + "\t" + (info.getInstalledBy() == null ? "" : info.getInstalledBy()) + "\t" + (info.getScript() == null ? "" : info.getScript()) + "\t" + info.getChecksum() + "\t" + info.getInstalledRank());
                    }
                }
                case "validate" -> {
                    var result = flyway.validateWithResult();
                    System.out.println("RESULT\tvalidate\tvalid=" + result.validationSuccessful + "\tinvalidMigrations=" + result.invalidMigrations.size() + "\tvalidateCount=" + result.validateCount);
                    for (var invalid : result.invalidMigrations) {
                        System.out.println("INVALID\t" + invalid.version + "\t" + invalid.description + "\t" + invalid.filepath + "\t" + invalid.errorDetails.errorCode + "\t" + invalid.errorDetails.errorMessage);
                    }
                }
                case "clean" -> {
                    flyway.clean();
                    System.out.println("RESULT\tclean\tsuccess=true");
                }
                case "baseline" -> {
                    var result = flyway.baseline();
                    System.out.println("RESULT\tbaseline\tsuccess=" + result.successfullyBaselined + "\tbaselineVersion=" + result.baselineVersion);
                }
                case "repair" -> {
                    var result = flyway.repair();
                    System.out.println("RESULT\trepair\trepairActions=" + result.repairActions.size() + "\talignedMigrations=" + result.migrationsAligned.size());
                    for (var repairAction : result.repairActions) System.out.println("REPAIR\t" + repairAction);
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
