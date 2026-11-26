package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTOMLFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadTOMLFixture(t *testing.T, name, content string) ConfigurationModel {
	t.Helper()
	model, err := LoadTOMLConfigurationFile(writeTOMLFixture(t, name, content))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestLoadTOMLConfigurationFileModernEnvironmentAndTypes(t *testing.T) {
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
environment = "prod"

[environments.prod]
url = "jdbc:postgresql://localhost/prod"
user = "prod_user"
password = "prod_password"
schemas = ["app", "audit"]
jdbcProperties = { ssl = "true", connectTimeout = "5" }

[environments.prod.flyway]
locations = ["filesystem:migrations/prod"]
outOfOrder = false
connectRetries = 0
placeholders = { schema = "prod" }
`)
	cfg, err := DefaultConfigurationModel().Merge(model).ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ConfigModeModern || cfg.EnvironmentName != "prod" {
		t.Fatalf("mode/environment = %v/%q", cfg.Mode, cfg.EnvironmentName)
	}
	if cfg.URL != "jdbc:postgresql://localhost/prod" || cfg.User != "prod_user" || cfg.Password != "prod_password" {
		t.Fatalf("environment connection = %#v", cfg)
	}
	if strings.Join(cfg.Schemas, "|") != "app|audit" {
		t.Fatalf("schemas = %#v", cfg.Schemas)
	}
	if cfg.JDBCProperties["ssl"] != "true" || cfg.JDBCProperties["connectTimeout"] != "5" {
		t.Fatalf("jdbcProperties = %#v", cfg.JDBCProperties)
	}
	if len(cfg.Locations) != 1 || cfg.Locations[0] != "filesystem:migrations/prod" {
		t.Fatalf("locations = %#v", cfg.Locations)
	}
	if cfg.OutOfOrder || cfg.ConnectRetries != 0 || cfg.Placeholders["schema"] != "prod" {
		t.Fatalf("typed values = %#v", cfg)
	}
}

func TestTOMLConfigurationMergeOrderAndEmptyLists(t *testing.T) {
	base := loadTOMLFixture(t, "base.toml", `
[flyway]
locations = ["filesystem:base"]
callbacks = ["base_callback"]
placeholders = { a = "base", keep = "base" }

[environments.prod.flyway]
locations = ["filesystem:env"]
callbacks = ["env_callback"]
placeholders = { a = "env" }
`)
	override := loadTOMLFixture(t, "override.toml", `
[flyway]
locations = []
callbacks = []
placeholders = { a = "override" }

[environments.prod.flyway]
locations = []
callbacks = []
placeholders = { a = "override-env" }
`)
	model := base.Merge(override)
	if len(stringListValue(model.Flyway["locations"])) != 1 || stringListValue(model.Flyway["locations"])[0] != "filesystem:base" {
		t.Fatalf("root locations = %#v", model.Flyway["locations"])
	}
	rootPlaceholders := asStringMap(model.Flyway["placeholders"])
	if rootPlaceholders["a"] != "override" || rootPlaceholders["keep"] != "base" {
		t.Fatalf("root placeholders = %#v", rootPlaceholders)
	}
	envFlyway := asStringMap(model.Environments["prod"]["flyway"])
	if len(stringListValue(envFlyway["locations"])) != 1 || stringListValue(envFlyway["locations"])[0] != "filesystem:env" {
		t.Fatalf("environment locations = %#v", envFlyway["locations"])
	}
	envPlaceholders := asStringMap(envFlyway["placeholders"])
	if envPlaceholders["a"] != "override-env" {
		t.Fatalf("environment placeholders = %#v", envPlaceholders)
	}
}

func TestTOMLBaselineMigrationPrefix(t *testing.T) {
	cfg, err := DefaultConfigurationModel().ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Naming.BaselinePrefix != "B" {
		t.Fatalf("default baselineMigrationPrefix = %q, want B", cfg.Naming.BaselinePrefix)
	}

	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
baselineMigrationPrefix = "BB"
`)
	cfg, err = DefaultConfigurationModel().Merge(model).ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Naming.BaselinePrefix != "BB" {
		t.Fatalf("configured baselineMigrationPrefix = %q, want BB", cfg.Naming.BaselinePrefix)
	}
}

func TestTOMLConfigurationRootThenEnvironmentThenTemporary(t *testing.T) {
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
environment = "prod"
table = "root_table"
locations = ["filesystem:root"]

[environments.prod]
url = "jdbc:postgresql://localhost/env"
user = "env_user"

[environments.prod.flyway]
table = "env_table"
locations = ["filesystem:env"]
`)
	temp, err := ConfigurationModelFromFlatMap(map[string]string{
		"environments.tempConfigEnvironment.url":      "jdbc:postgresql://localhost/temp",
		"environments.tempConfigEnvironment.user":     "temp_user",
		"environments.tempConfigEnvironment.password": "temp_password",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := model.Merge(temp).ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "jdbc:postgresql://localhost/temp" || cfg.User != "temp_user" || cfg.Password != "temp_password" {
		t.Fatalf("temporary environment did not win: %#v", cfg)
	}
	if cfg.Table != "env_table" || len(cfg.Locations) != 1 || cfg.Locations[0] != "filesystem:env" {
		t.Fatalf("root/environment order = %#v", cfg)
	}
	if _, ok := model.Merge(temp).MergeTemporaryEnvironment().Environments["tempConfigEnvironment"]; ok {
		t.Fatal("tempConfigEnvironment must be removed after merge")
	}
}

func TestTOMLConfigurationTargetOptionalAndFalseZero(t *testing.T) {
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
target = "2?"
outOfOrder = false
connectRetries = 0
cleanDisabled = false
locations = ["filesystem:migrations"]
`)
	cfg, err := DefaultConfigurationModel().Merge(model).ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TargetVersion != "2" || cfg.FailOnMissingTarget {
		t.Fatalf("target = %q failOnMissingTarget=%v", cfg.TargetVersion, cfg.FailOnMissingTarget)
	}
	if cfg.OutOfOrder || cfg.ConnectRetries != 0 || cfg.CleanDisabled == nil || *cfg.CleanDisabled {
		t.Fatalf("explicit false/zero lost: %#v", cfg)
	}
}

func TestModernTargetFailOnMissingTargetSemantics(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		want     string
		wantFail bool
	}{
		{name: "default", want: "latest", wantFail: true},
		{name: "empty", content: "[flyway]\ntarget = \"\"\n", want: "latest", wantFail: true},
		{name: "plain", content: "[flyway]\ntarget = \"4\"\n", want: "4", wantFail: true},
		{name: "optional", content: "[flyway]\ntarget = \"4?\"\n", want: "4", wantFail: false},
		{name: "latest optional", content: "[flyway]\ntarget = \"latest?\"\n", want: "latest", wantFail: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := ConfigurationModel{}
			if tc.content != "" {
				model = loadTOMLFixture(t, "flyway.toml", tc.content)
			}
			cfg, err := DefaultConfigurationModel().Merge(model).ToConfig("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TargetVersion != tc.want || cfg.FailOnMissingTarget != tc.wantFail {
				t.Fatalf("target = (%q,%v), want (%q,%v)", cfg.TargetVersion, cfg.FailOnMissingTarget, tc.want, tc.wantFail)
			}
			cfg.ApplyDefaultsModern()
			if cfg.TargetVersion != tc.want || cfg.FailOnMissingTarget != tc.wantFail {
				t.Fatalf("idempotent target = (%q,%v), want (%q,%v)", cfg.TargetVersion, cfg.FailOnMissingTarget, tc.want, tc.wantFail)
			}
		})
	}
}

func TestExplicitFailOnMissingTargetIsUnknown(t *testing.T) {
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
target = "4?"
failOnMissingTarget = false
`)
	_, err := DefaultConfigurationModel().Merge(model).ToConfig("")
	if err == nil || !strings.Contains(err.Error(), "Unknown configuration property: flyway.failOnMissingTarget") {
		t.Fatalf("error = %v, want unknown failOnMissingTarget", err)
	}
}

func TestFailOnMissingTargetEnvironmentVariableIsNotMapped(t *testing.T) {
	env := EnvironmentConfigurationFrom(map[string]string{
		"FLYWAY_FAIL_ON_MISSING_TARGET": "false",
	})
	if _, ok := env["flyway.failOnMissingTarget"]; ok {
		t.Fatalf("environment configuration unexpectedly mapped failOnMissingTarget: %#v", env)
	}
}
func TestTOMLConfigurationRelativeFilesystemPaths(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
locations = ["filesystem:migrations", "classpath:db/migration"]
callbackLocations = ["filesystem:callbacks"]
jarDirs = ["jars", "C:/absolute/jars"]
`)
	cfg, err := DefaultConfigurationModel().Merge(model).ToConfig(work)
	if err != nil {
		t.Fatal(err)
	}
	wantLocation := "filesystem:" + filepath.Clean(filepath.Join(work, "migrations"))
	if cfg.Locations[0] != wantLocation || cfg.Locations[1] != "classpath:db/migration" {
		t.Fatalf("locations = %#v, want first %q", cfg.Locations, wantLocation)
	}
	wantCallbacks := "filesystem:" + filepath.Clean(filepath.Join(work, "callbacks"))
	if cfg.CallbackLocations[0] != wantCallbacks {
		t.Fatalf("callback locations = %#v, want %q", cfg.CallbackLocations, wantCallbacks)
	}
	if cfg.JarDirs[0] != filepath.Clean(filepath.Join(work, "jars")) {
		t.Fatalf("relative jarDir = %q", cfg.JarDirs[0])
	}
	if cfg.JarDirs[1] != filepath.Clean("C:/absolute/jars") {
		t.Fatalf("absolute jarDir changed = %q", cfg.JarDirs[1])
	}
}

func TestModernEnvironmentVariableMapping(t *testing.T) {
	model, warnings, err := ModernEnvironmentConfigurationFrom(map[string]string{
		"flyway_locations":                    "filesystem:lower",
		"flyway_url":                          "jdbc:postgresql://localhost/lower",
		"FLYWAY_URL":                          "jdbc:postgresql://localhost/upper",
		"FLYWAY_CONFIG_FILES":                 "must-not-be-a-property.toml",
		"Flyway_URL":                          "ignored",
		"environments_PROD_url":               "jdbc:postgresql://localhost/prod",
		"environments_PROD_environmentGroups": "prod,staging",
	}, []string{"prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Using both new Environment variable flyway_url and old Environment variable FLYWAY_URL") {
		t.Fatalf("warnings = %#v", warnings)
	}
	root := model.Flyway
	locations := stringListValue(root["locations"])
	if len(locations) != 1 || locations[0] != "filesystem:lower" {
		t.Fatalf("root locations = %#v", locations)
	}
	temp := model.Environments["tempConfigEnvironment"]
	if stringValue(temp["url"]) != "jdbc:postgresql://localhost/lower" {
		t.Fatalf("temp url = %#v", temp["url"])
	}
	prod := model.Environments["prod"]
	if stringValue(prod["url"]) != "jdbc:postgresql://localhost/prod" {
		t.Fatalf("prod url = %#v", prod["url"])
	}
	if groups := stringListValue(prod["environmentGroups"]); strings.Join(groups, "|") != "prod|staging" {
		t.Fatalf("prod groups = %#v", groups)
	}
	if _, ok := root["configFiles"]; ok {
		t.Fatalf("FLYWAY_CONFIG_FILES leaked into model: %#v", root)
	}
}

func TestModernEnvironmentVariableCasingAndConflicts(t *testing.T) {
	model, warnings, err := ModernEnvironmentConfigurationFrom(map[string]string{
		"environments_prod_url": "jdbc:postgresql://localhost/lowercase-name",
	}, []string{"Prod"})
	if err != nil {
		t.Fatal(err)
	}
	prod, ok := model.Environments["Prod"]
	if !ok || stringValue(prod["url"]) != "jdbc:postgresql://localhost/lowercase-name" {
		t.Fatalf("case correction failed: %#v warnings=%#v", model.Environments, warnings)
	}
	if _, _, err := ModernEnvironmentConfigurationFrom(map[string]string{
		"environments_PROD_url": "x",
		"environments_Prod_url": "y",
	}, []string{"prod", "Prod"}); err == nil {
		t.Fatal("expected ambiguous environment casing error")
	}
}

func TestModernEnvironmentFieldsAndFlatMap(t *testing.T) {
	model, err := ConfigurationModelFromFlatMap(map[string]string{
		"flyway.environment":                       "prod",
		"environments.prod.displayName":            "Production",
		"environments.prod.provisioner":            "kubernetes",
		"environments.prod.resolvers.azure.kind":   "azure",
		"environments.prod.resolvers.azure.tenant": "tenant-1",
		"environments.prod.environmentGroups":      "prod,west",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DefaultConfigurationModel().Merge(model).ToConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DisplayName != "Production" || cfg.Provisioner != "kubernetes" {
		t.Fatalf("environment fields = %#v", cfg)
	}
	if cfg.Resolvers["azure"]["kind"] != "azure" || cfg.Resolvers["azure"]["tenant"] != "tenant-1" {
		t.Fatalf("resolvers = %#v", cfg.Resolvers)
	}
	if strings.Join(cfg.EnvironmentGroups, "|") != "prod|west" {
		t.Fatalf("environmentGroups = %#v", cfg.EnvironmentGroups)
	}
}

func TestModernDefaultSQLocationAndJarDirs(t *testing.T) {
	install := t.TempDir()
	if err := os.Mkdir(filepath.Join(install, "sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "sql", "V1__init.sql"), []byte("SELECT 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	cfg, err := DefaultConfigurationModel().ToConfigWithInstallationDirectory("", install, func(format string, args ...interface{}) {
		warnings = append(warnings, format)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "filesystem:" + filepath.Clean(filepath.Join(install, "sql"))
	if len(cfg.Locations) != 1 || cfg.Locations[0] != want {
		t.Fatalf("locations = %#v, want %q", cfg.Locations, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Storing migrations in 'sql'") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestModernEnvironmentCaseInsensitiveSuggestion(t *testing.T) {
	model := loadTOMLFixture(t, "flyway.toml", `
[flyway]
environment = "prod"

[environments.Prod]
url = "jdbc:postgresql://localhost/prod"
`)
	_, err := model.ToConfig("")
	if err == nil || !strings.Contains(err.Error(), "Environment 'prod' not found") || !strings.Contains(err.Error(), "Did you mean 'Prod'?") {
		t.Fatalf("error = %v", err)
	}
}
