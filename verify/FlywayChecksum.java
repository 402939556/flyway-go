import java.io.BufferedReader;
import java.io.FileInputStream;
import java.io.InputStreamReader;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.util.zip.CRC32;

/**
 * 精确复刻 flyway-core ChecksumCalculator.calculateChecksumForResource 的行为，
 * 用于与 Go 实现做逐位交叉验证。
 */
public class FlywayChecksum {

    // 复刻 BomFilter.FilterBomFromString：剥离字符串开头的 BOM
    static String filterBomFromString(String s) {
        if (s == null) {
            return null;
        }
        if (s.startsWith("\uFEFF")) {
            return s.substring(1);
        }
        return s;
    }

    // 与官方实现逐行对照
    static int calculateChecksum(String str) throws Exception {
        final CRC32 crc32 = new CRC32();
        BufferedReader bufferedReader = new BufferedReader(new StringReader(str));
        String line = bufferedReader.readLine();
        if (line != null) {
            line = filterBomFromString(line);
            do {
                crc32.update(line.getBytes(StandardCharsets.UTF_8));
            } while ((line = bufferedReader.readLine()) != null);
        }
        return (int) crc32.getValue();
    }

    public static void main(String[] args) throws Exception {
        if (args.length == 0) {
            // 从 stdin 读取：第一行是文件路径，后续逐个文件输出 checksum
            BufferedReader in = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
            String path;
            while ((path = in.readLine()) != null) {
                path = path.trim();
                if (path.isEmpty()) {
                    continue;
                }
                String content = readFile(path);
                System.out.println(path + "\t" + calculateChecksum(content));
            }
        } else {
            String content = readFile(args[0]);
            System.out.println(calculateChecksum(content));
        }
    }

    static String readFile(String path) throws Exception {
        StringBuilder sb = new StringBuilder();
        try (BufferedReader r = new BufferedReader(
                new InputStreamReader(new FileInputStream(path), StandardCharsets.UTF_8), 4096)) {
            char[] buf = new char[4096];
            int n;
            while ((n = r.read(buf)) > 0) {
                sb.append(buf, 0, n);
            }
        }
        return sb.toString();
    }
}
