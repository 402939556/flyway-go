package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEnvironmentConfigurationFromMapsOfficialKeys(t *testing.T) {
	got := EnvironmentConfigurationFrom(map[string]string{
		"FLYWAY_URL":                         "jdbc:postgresql://localhost/db",
		"FLYWAY_BASELINE_MIGRATION_PREFIX":   "BB",
		"FLYWAY_PLACEHOLDERS_SCHEMA":         "app",
		"FLYWAY_PLACEHOLDERS_TABLE_NAME":     "orders",
		"FLYWAY_JDBC_PROPERTIES_SSL":         "true",
		"FLYWAY_JDBC_PROPERTIES_ACCESSTOKEN": "token",
		"UNRELATED":                          "ignored",
	})
	want := map[string]string{
		"flyway.url":                        "jdbc:postgresql://localhost/db",
		"flyway.baselineMigrationPrefix":    "BB",
		"flyway.placeholders.schema":        "app",
		"flyway.placeholders.table_name":    "orders",
		"flyway.jdbcProperties.ssl":         "true",
		"flyway.jdbcProperties.accessToken": "token",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q (all=%#v)", k, got[k], v, got)
		}
	}
	if _, ok := got["UNRELATED"]; ok {
		t.Fatalf("unexpected key in %#v", got)
	}
}

func TestConvertEnvironmentKeyDoesNotLowercaseFixedNames(t *testing.T) {
	if got := convertEnvironmentKey("flyway.url"); got != "" {
		t.Fatalf("lowercase env key converted to %q", got)
	}
	if got := convertEnvironmentKey("FLYWAY_PLACEHOLDERS_Mixed-Case"); got != "flyway.placeholders.mixed-case" {
		t.Fatalf("placeholder key = %q", got)
	}
	if got := convertEnvironmentKey("FLYWAY_PLACEHOLDERS_"); got != "" {
		t.Fatalf("empty placeholder suffix converted to %q", got)
	}
}

func TestDefaultConfigurationFilesOrderAndWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	wd := filepath.Join(t.TempDir(), "work")
	install := filepath.Join(t.TempDir(), "install")
	got := DefaultConfigurationFiles(install, wd)
	want := []string{
		filepath.Join(install, "conf", "flyway.conf"),
		filepath.Join(home, "flyway.conf"),
		"flyway.conf",
		filepath.Join(wd, "flyway.conf"),
	}
	if len(got) != len(want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("files[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadDefaultConfigurationFilesLaterLayersOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	install := filepath.Join(t.TempDir(), "install")
	wd := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Join(install, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	writeUTF8 := func(path, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUTF8(filepath.Join(install, "conf", "flyway.conf"), "flyway.url=install\nflyway.table=install_table\n")
	writeUTF8(filepath.Join(home, "flyway.conf"), "flyway.url=home\nflyway.user=home_user\n")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	writeUTF8("flyway.conf", "flyway.url=cwd\nflyway.password=cwd_password\n")
	writeUTF8(filepath.Join(wd, "flyway.conf"), "flyway.url=work\nflyway.table=work_table\n")
	got, err := LoadDefaultConfigurationFiles(install, wd, "UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	if got["flyway.url"] != "work" || got["flyway.table"] != "work_table" || got["flyway.user"] != "home_user" || got["flyway.password"] != "cwd_password" {
		t.Fatalf("merged configuration = %#v", got)
	}
}

func TestLoadConfigurationFromStringJavaPropertiesAndEnvironmentExpansion(t *testing.T) {
	t.Setenv("PARITY_SECRET", "p@ss${not-recursive}\\value")
	configuration := "# comment\r\nflyway.url = jdbc:postgresql://localhost/db\r\n" +
		"flyway.placeholders.a=${PARITY_SECRET}\r\n" +
		"flyway.escaped=one=two\r\n" +
		"flyway.unicode=\u4F60\u597D\r\n"
	got, err := LoadConfigurationFromString(configuration, false)
	if err != nil {
		t.Fatal(err)
	}
	if got["flyway.url"] != "jdbc:postgresql://localhost/db" {
		t.Fatalf("url = %q", got["flyway.url"])
	}
	if got["flyway.placeholders.a"] != "p@ss${not-recursive}value" {
		t.Fatalf("expanded = %q", got["flyway.placeholders.a"])
	}
	if got["flyway.escaped"] != "one=two" {
		t.Fatalf("escaped = %q", got["flyway.escaped"])
	}
	if got["flyway.unicode"] != "你好" {
		t.Fatalf("unicode = %q", got["flyway.unicode"])
	}
}

func TestLoadConfigurationFromReaderUTF8AndUTF16(t *testing.T) {
	utf8Path := filepath.Join(t.TempDir(), "utf8.conf")
	if err := os.WriteFile(utf8Path, []byte("flyway.placeholders.name=值\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfigurationFile(utf8Path, "UTF-8", true)
	if err != nil {
		t.Fatal(err)
	}
	if got["flyway.placeholders.name"] != "值" {
		t.Fatalf("utf8 = %#v", got)
	}

	utf16Path := filepath.Join(t.TempDir(), "utf16.conf")
	u := utf16.Encode([]rune("flyway.placeholders.name=值\n"))
	raw := []byte{0xff, 0xfe}
	for _, value := range u {
		raw = append(raw, byte(value), byte(value>>8))
	}
	if err := os.WriteFile(utf16Path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadConfigurationFile(utf16Path, "UTF-16", true)
	if err != nil {
		t.Fatal(err)
	}
	if got["flyway.placeholders.name"] != "值" {
		t.Fatalf("utf16 = %#v", got)
	}
}

func TestApplyConfigurationValuesPlaceholdersAndJDBCProperties(t *testing.T) {
	cfg := Config{}
	err := ApplyConfiguration(&cfg, map[string]string{
		"flyway.url":                     "postgres://u:p@localhost/db",
		"flyway.user":                    "u",
		"flyway.password":                "p",
		"flyway.driver":                  "postgres",
		"flyway.locations":               "filesystem:migrations, classpath:db/migration",
		"flyway.callbackLocations":       "callbacks",
		"flyway.baselineOnMigrate":       "TRUE",
		"flyway.skipExecutingMigrations": "true",
		"flyway.baselineMigrationPrefix": "BB",
		"flyway.cleanDisabled":           "false",
		"flyway.validateOnMigrate":       "FALSE",
		"flyway.connectRetries":          "3",
		"flyway.connectRetriesInterval":  "0",
		"flyway.lockRetryCount":          "7",
		"flyway.target":                  "2.1",
		"flyway.placeholders.schema":     "app",
		"flyway.jdbcProperties.ssl":      "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "postgres://u:p@localhost/db" || cfg.User != "u" || cfg.Password != "p" || cfg.Driver != "postgres" {
		t.Fatalf("connection config = %#v", cfg)
	}
	if strings.Join(cfg.Locations, "|") != "filesystem:migrations|classpath:db/migration" {
		t.Fatalf("locations = %#v", cfg.Locations)
	}
	if strings.Join(cfg.CallbackLocations, "|") != "callbacks" {
		t.Fatalf("callbacks = %#v", cfg.CallbackLocations)
	}
	if !cfg.BaselineOnMigrate || !cfg.SkipExecutingMigrations || cfg.CleanDisabled == nil || *cfg.CleanDisabled || cfg.ValidateOnMigrate == nil || *cfg.ValidateOnMigrate {
		t.Fatalf("bool config = %#v", cfg)
	}
	if cfg.ConnectRetries != 3 || cfg.ConnectRetriesInterval == nil || *cfg.ConnectRetriesInterval != 0 || cfg.LockRetryCount == nil || *cfg.LockRetryCount != 7 {
		t.Fatalf("int config = %#v", cfg)
	}
	if cfg.Naming.BaselinePrefix != "BB" {
		t.Fatalf("baselineMigrationPrefix = %q", cfg.Naming.BaselinePrefix)
	}
	if cfg.TargetVersion != "2.1" {
		t.Fatalf("target = %q", cfg.TargetVersion)
	}
	if cfg.Placeholders["schema"] != "app" || cfg.JDBCProperties["ssl"] != "true" {
		t.Fatalf("maps = %#v / %#v", cfg.Placeholders, cfg.JDBCProperties)
	}
}

func TestApplyConfigurationRejectsInvalidAndUnknownValues(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]string
		want  string
	}{
		{"boolean whitespace", map[string]string{"flyway.cleanDisabled": " true"}, "Invalid value for flyway.cleanDisabled"},
		{"boolean junk", map[string]string{"flyway.cleanDisabled": "yes"}, "Invalid value for flyway.cleanDisabled"},
		{"integer whitespace", map[string]string{"flyway.connectRetries": " 1"}, "Invalid value for flyway.connectRetries"},
		{"integer junk", map[string]string{"flyway.connectRetries": "one"}, "Invalid value for flyway.connectRetries"},
		{"unknown", map[string]string{"flyway.notARealSetting": "x"}, "Unknown configuration property: flyway.notARealSetting. Please check your conf files or commandline parameters"},
		{"failOnMissingTarget", map[string]string{"flyway.failOnMissingTarget": "false"}, "Unknown configuration property: flyway.failOnMissingTarget. Please check your conf files or commandline parameters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			err := ApplyConfiguration(&cfg, tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestWorkingDirectoryDoesNotRewriteFilesystemLocations(t *testing.T) {
	cfg := Config{
		WorkingDirectory:  filepath.Join(t.TempDir(), "work"),
		Locations:         []string{"filesystem:migrations", "classpath:db/migration"},
		CallbackLocations: []string{"filesystem:callbacks"},
	}
	cfg.ApplyDefaults()
	if strings.Join(cfg.Locations, "|") != "filesystem:migrations|classpath:db/migration" {
		t.Fatalf("locations changed: %#v", cfg.Locations)
	}
	if strings.Join(cfg.CallbackLocations, "|") != "filesystem:callbacks" {
		t.Fatalf("callback locations changed: %#v", cfg.CallbackLocations)
	}
}
