import org.flywaydb.core.api.configuration.FluentConfiguration;
import org.flywaydb.core.internal.resource.ResourceName;
import org.flywaydb.core.internal.resource.ResourceNameParser;

public class NameProbe {
  public static void main(String[] args) {
    FluentConfiguration cfg = new FluentConfiguration();
    ResourceNameParser p = new ResourceNameParser(cfg);
    for (String name : args) {
      ResourceName r = p.parse(name);
      System.out.print(name + "\tvalid=" + r.isValid() + "\tmsg=" + r.getValidityMessage());
      if (r.isValid()) {
        System.out.print("\ttype=" + r.getType() + "\tprefix=" + r.getPrefix() + "\tversion=" + r.getVersion()
          + "\tdesc=[" + r.getDescription() + "]\tsuffix=" + r.getSuffix());
      }
      System.out.println();
    }
  }
}
