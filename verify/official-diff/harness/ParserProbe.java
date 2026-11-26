import org.flywaydb.core.api.configuration.FluentConfiguration;
import org.flywaydb.core.internal.parser.ParsingContext;
import org.flywaydb.core.internal.resource.StringResource;
import org.flywaydb.core.internal.sqlscript.SqlStatement;
import org.flywaydb.core.internal.sqlscript.SqlStatementIterator;
import org.flywaydb.core.internal.sqlscript.ParserSqlScript;
import org.flywaydb.core.internal.parser.Parser;
import org.flywaydb.database.postgresql.PostgreSQLParser;
import org.flywaydb.database.mysql.MySQLParser;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

public class ParserProbe {
  static String b64(String s) { return s == null ? "~" : Base64.getEncoder().encodeToString(s.getBytes(StandardCharsets.UTF_8)); }
  public static void main(String[] args) throws Exception {
    String dialect = args[0];
    String path = args[1];
    boolean mixed = args.length > 2 && Boolean.parseBoolean(args[2]);
    String content = new String(java.nio.file.Files.readAllBytes(java.nio.file.Paths.get(path)), StandardCharsets.UTF_8);
    FluentConfiguration cfg = new FluentConfiguration().mixed(mixed);
    ParsingContext ctx = new ParsingContext();
    Parser parser = "postgresql".equals(dialect)
      ? new PostgreSQLParser(cfg, ctx)
      : new MySQLParser(cfg, ctx);
    ParserSqlScript script = new ParserSqlScript(parser, new StringResource(content), null, mixed);
    int n = 0;
    try (SqlStatementIterator it = script.getSqlStatements()) {
      while (it.hasNext()) {
        SqlStatement s = it.next();
        n++;
        System.out.println(n + "\t" + s.getLineNumber() + "\t" + s.canExecuteInTransaction() + "\t" + s.getDelimiter() + "\t" + b64(s.getSql()));
      }
    } catch (Throwable t) {
      System.out.println("ERROR\t" + t.getClass().getName() + "\t" + b64(t.getMessage()));
    }
  }
}
