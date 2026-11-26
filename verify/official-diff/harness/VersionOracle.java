import org.flywaydb.core.api.MigrationVersion;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

/**
 * Official Flyway 13.13.0 oracle for MigrationVersion differential testing.
 * Protocol: parse <input> | compare <left> <right>
 * "<NULL>" represents Java null. String fields are base64-encoded.
 */
public final class VersionOracle {
    private static String b64(String value) {
        return value == null ? "~" : Base64.getEncoder().encodeToString(value.getBytes(StandardCharsets.UTF_8));
    }

    private static String input(String value) {
        return "<NULL>".equals(value) ? null : value;
    }

    private static String describe(String value) {
        try {
            MigrationVersion v = MigrationVersion.fromVersion(input(value));
            return "ok\t" + b64(v.toString()) + "\t" + b64(v.getVersion()) + "\t" + b64(v.getName()) + "\t"
                    + b64(v.getRawVersion()) + "\t" + v.isPredefined() + "\t" + v.getMajorAsString() + "\t"
                    + v.getMinorAsString();
        } catch (Throwable t) {
            return "error\t" + t.getClass().getName() + "\t" + b64(t.getMessage());
        }
    }

    public static void main(String[] args) {
        if (args.length >= 2 && "parse".equals(args[0])) {
            for (int i = 1; i < args.length; i++) {
                System.out.println(b64(input(args[i])) + "\t" + describe(args[i]));
            }
            return;
        }
        if (args.length >= 3 && "compare".equals(args[0])) {
            try {
                MigrationVersion left = MigrationVersion.fromVersion(input(args[1]));
                MigrationVersion right = MigrationVersion.fromVersion(input(args[2]));
                System.out.println("ok\t" + Integer.signum(left.compareTo(right)) + "\t" + left.equals(right));
            } catch (Throwable t) {
                System.out.println("error\t" + t.getClass().getName() + "\t" + b64(t.getMessage()));
            }
            return;
        }
        System.err.println("usage: VersionOracle parse <value>... | compare <left> <right>");
        System.exit(2);
    }
}
