package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linsu/flywaygo/core"
)

func modernResolverDependencies(installationDirectory string) cliDependencies {
	return cliDependencies{
		loadDefaultTOML: func(installationDirectory, workingDirectory string) (core.ConfigurationModel, error) {
			return core.ConfigurationModel{}, nil
		},
		loadTOMLFiles: func(paths []string) (core.ConfigurationModel, error) {
			return core.ConfigurationModel{}, nil
		},
		installationDirectory: func() string { return installationDirectory },
	}
}

func TestConfigurationFilePathsUseCaseSensitiveTomlSuffix(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	raw := "one.toml,two.TOML,three.Toml,four.conf"

	tomlPaths := configFilePathsForMode(raw, work, true)
	wantToml := []string{core.AbsoluteFilePath(filepath.Join(work, "one.toml"))}
	if strings.Join(tomlPaths, "|") != strings.Join(wantToml, "|") {
		t.Fatalf("TOML paths = %#v, want %#v", tomlPaths, wantToml)
	}

	legacyPaths := configFilePathsForMode(raw, work, false)
	wantLegacy := []string{
		core.AbsoluteFilePath(filepath.Join(work, "two.TOML")),
		core.AbsoluteFilePath(filepath.Join(work, "three.Toml")),
		core.AbsoluteFilePath(filepath.Join(work, "four.conf")),
	}
	if strings.Join(legacyPaths, "|") != strings.Join(wantLegacy, "|") {
		t.Fatalf("legacy paths = %#v, want %#v", legacyPaths, wantLegacy)
	}
}

func TestSelectConfigurationModeExplicitAndEnvironment(t *testing.T) {
	work := t.TempDir()
	tests := []struct {
		name            string
		explicitRaw     string
		environmentRaw  string
		existing        []string
		wantMode        core.ConfigMode
		wantWarningText string
		wantErrorText   string
	}{
		{
			name:        "explicit TOML",
			explicitRaw: "explicit.toml",
			existing:    []string{"explicit.toml"},
			wantMode:    core.ConfigModeModern,
		},
		{
			name:        "explicit CONF",
			explicitRaw: "explicit.conf",
			existing:    []string{"explicit.conf"},
			wantMode:    core.ConfigModeClassic,
		},
		{
			name:        "uppercase TOML suffix is legacy",
			explicitRaw: "explicit.TOML",
			existing:    []string{"explicit.TOML"},
			wantMode:    core.ConfigModeClassic,
		},
		{
			name:          "explicit coexisting TOML and CONF",
			explicitRaw:   "explicit.toml,explicit.conf",
			existing:      []string{"explicit.toml", "explicit.conf"},
			wantErrorText: "Using both TOML configuration and CONF configuration is not supported",
		},
		{
			name:            "explicit missing file is warned but existing TOML decides",
			explicitRaw:     "explicit.toml,missing.conf",
			existing:        []string{"explicit.toml"},
			wantMode:        core.ConfigModeModern,
			wantWarningText: "One or more specified configuration files could not be found",
		},
		{
			name:           "environment TOML decides before existence filtering",
			environmentRaw: "environment.toml",
			wantMode:       core.ConfigModeModern,
		},
		{
			name:           "environment CONF decides before existence filtering",
			environmentRaw: "environment.conf",
			wantMode:       core.ConfigModeClassic,
		},
		{
			name:           "environment coexisting TOML and CONF",
			environmentRaw: "environment.toml,environment.conf",
			wantErrorText:  "Using both TOML configuration and CONF configuration is not supported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			existing := map[string]bool{}
			for _, name := range tc.existing {
				existing[filepath.Join(work, name)] = true
			}
			rawEnvironment := map[string]string{}
			if tc.environmentRaw != "" {
				rawEnvironment["FLYWAY_CONFIG_FILES"] = tc.environmentRaw
			}
			deps := modernResolverDependencies(work)
			deps.fileExists = func(path string) bool { return existing[path] }
			deps.rawEnvironment = func() map[string]string { return rawEnvironment }

			var stderr bytes.Buffer
			selection, err := selectConfigurationMode(deps, tc.explicitRaw, work, &stderr)
			if tc.wantErrorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrorText) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErrorText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if selection.mode != tc.wantMode {
				t.Fatalf("mode = %v, want %v", selection.mode, tc.wantMode)
			}
			if tc.wantWarningText != "" && !strings.Contains(stderr.String(), tc.wantWarningText) {
				t.Fatalf("stderr = %q, want containing %q", stderr.String(), tc.wantWarningText)
			}
		})
	}
}

func TestSelectConfigurationModeDefaultFiles(t *testing.T) {
	install := filepath.Join(t.TempDir(), "install")
	work := filepath.Join(t.TempDir(), "work")
	confPath := core.AbsoluteFilePath(filepath.Join(install, "conf", "flyway.conf"))
	tomlPath := core.AbsoluteFilePath(filepath.Join(install, "conf", "flyway.toml"))

	t.Run("TOML wins when both default forms exist", func(t *testing.T) {
		deps := modernResolverDependencies(install)
		deps.fileExists = func(path string) bool { return path == confPath || path == tomlPath }
		selection, err := selectConfigurationMode(deps, "", work, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if selection.mode != core.ConfigModeModern {
			t.Fatalf("mode = %v, want Modern", selection.mode)
		}
		if strings.Join(selection.defaultToml, "|") != tomlPath {
			t.Fatalf("default TOML = %#v", selection.defaultToml)
		}
	})

	t.Run("CONF default selects Classic", func(t *testing.T) {
		deps := modernResolverDependencies(install)
		deps.fileExists = func(path string) bool { return path == confPath }
		selection, err := selectConfigurationMode(deps, "", work, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if selection.mode != core.ConfigModeClassic {
			t.Fatalf("mode = %v, want Classic", selection.mode)
		}
	})

	t.Run("no default files selects Modern", func(t *testing.T) {
		deps := modernResolverDependencies(install)
		deps.fileExists = func(path string) bool { return false }
		selection, err := selectConfigurationMode(deps, "", work, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if selection.mode != core.ConfigModeModern {
			t.Fatalf("mode = %v, want Modern", selection.mode)
		}
	})

	t.Run("no modern loader falls back to Classic", func(t *testing.T) {
		deps := cliDependencies{
			installationDirectory: func() string { return install },
			fileExists:            func(path string) bool { return false },
		}
		selection, err := selectConfigurationMode(deps, "", work, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if selection.mode != core.ConfigModeClassic {
			t.Fatalf("mode = %v, want Classic", selection.mode)
		}
	})
}

func TestSelectConfigurationModeMissingExplicitFilesWarnWithAbsolutePathsInOrder(t *testing.T) {
	work := t.TempDir()
	missingTOML := core.AbsoluteFilePath(filepath.Join(work, "missing.toml"))
	missingCONF := core.AbsoluteFilePath(filepath.Join(work, "missing.conf"))
	deps := modernResolverDependencies(work)
	deps.fileExists = func(path string) bool { return false }

	var stderr bytes.Buffer
	selection, err := selectConfigurationMode(deps, "missing.toml,missing.conf", work, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if selection.mode != core.ConfigModeModern {
		t.Fatalf("mode = %v, want Modern", selection.mode)
	}
	want := fmt.Sprintf("One or more specified configuration files could not be found: %s, %s", missingTOML, missingCONF)
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want containing %q", stderr.String(), want)
	}
}

func TestResolveInvocationModernDynamicEnvironment(t *testing.T) {
	deps := modernResolverDependencies(t.TempDir())
	inv, code, stop := resolveInvocation([]string{
		"migrate",
		"-environment=prod",
		"-environments.prod.url=postgres://u:p@localhost:5432/prod_db",
		"-environments.prod.schemas=app,audit",
		"-environments.prod.flyway.table=prod_history",
		"-environments.prod.resolvers.azure.kind=azure",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	if inv.Config.Mode != core.ConfigModeModern || inv.Config.EnvironmentName != "prod" {
		t.Fatalf("mode/environment = %v/%q", inv.Config.Mode, inv.Config.EnvironmentName)
	}
	if inv.Config.URL != "postgres://u:p@localhost:5432/prod_db" {
		t.Fatalf("url = %q", inv.Config.URL)
	}
	if strings.Join(inv.Config.Schemas, "|") != "app|audit" {
		t.Fatalf("schemas = %#v", inv.Config.Schemas)
	}
	if inv.Config.Table != "prod_history" {
		t.Fatalf("table = %q", inv.Config.Table)
	}
	if inv.Config.Resolvers["azure"]["kind"] != "azure" {
		t.Fatalf("resolvers = %#v", inv.Config.Resolvers)
	}
}

func TestResolveInvocationModernTemporaryEnvironment(t *testing.T) {
	deps := modernResolverDependencies(t.TempDir())
	inv, code, stop := resolveInvocation([]string{
		"migrate",
		"-url=postgres://u:p@localhost:5432/temp_db",
		"-user=temp_user",
		"-password=temp_password",
		"-schemas=app,audit",
		"-baseline-migration-prefix=BB",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	if inv.Config.EnvironmentName != "default" {
		t.Fatalf("environment = %q", inv.Config.EnvironmentName)
	}
	if inv.Config.URL != "postgres://u:p@localhost:5432/temp_db" || inv.Config.User != "temp_user" || inv.Config.Password != "temp_password" {
		t.Fatalf("temp environment configuration = %#v", inv.Config)
	}
	if strings.Join(inv.Config.Schemas, "|") != "app|audit" {
		t.Fatalf("schemas = %#v", inv.Config.Schemas)
	}
	if inv.Config.Naming.BaselinePrefix != "BB" {
		t.Fatalf("baselineMigrationPrefix = %q, want BB", inv.Config.Naming.BaselinePrefix)
	}
}

func TestResolveInvocationClassicBaselineMigrationPrefix(t *testing.T) {
	deps := cliDependencies{
		loadDefaultConfig: func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
			return map[string]string{}, nil
		},
		environment: func() map[string]string { return map[string]string{} },
	}
	inv, code, stop := resolveInvocation([]string{
		"migrate",
		"-url", "postgres://u:p@localhost:5432/db",
		"-baseline-migration-prefix", "BB",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	if inv.Config.Mode != core.ConfigModeClassic {
		t.Fatalf("mode = %v, want Classic", inv.Config.Mode)
	}
	if inv.Config.Naming.BaselinePrefix != "BB" {
		t.Fatalf("baselineMigrationPrefix = %q, want BB", inv.Config.Naming.BaselinePrefix)
	}
}

func TestResolveInvocationDynamicEnvironmentErrors(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantText string
	}{
		{
			name:     "missing option",
			arg:      "-environments.prod=x",
			wantText: "Both the environment name and configuration option are required",
		},
		{
			name:     "unknown environment option",
			arg:      "-environments.prod.notARealSetting=x",
			wantText: "Failed to configure parameter: 'notARealSetting' in your 'prod' environment",
		},
		{
			name:     "resolver requires three segments",
			arg:      "-environments.prod.resolvers.azure=x",
			wantText: "Invalid resolver configuration for environment prod: resolvers.azure",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := modernResolverDependencies(t.TempDir())
			var stderr bytes.Buffer
			_, code, stop := resolveInvocation([]string{"migrate", tc.arg}, io.Discard, &stderr, deps)
			if !stop || code != 1 {
				t.Fatalf("stop=%v code=%d, want true/1", stop, code)
			}
			if !strings.Contains(stderr.String(), tc.wantText) {
				t.Fatalf("stderr = %q, want containing %q", stderr.String(), tc.wantText)
			}
		})
	}
}

func TestResolveInvocationSelectsConfigurationModeFromExplicitFile(t *testing.T) {
	work := t.TempDir()

	t.Run("TOML loads through Modern", func(t *testing.T) {
		tomlPath := core.AbsoluteFilePath(filepath.Join(work, "config.toml"))
		var loaded []string
		deps := modernResolverDependencies(work)
		deps.fileExists = func(path string) bool { return path == tomlPath }
		deps.loadTOMLFiles = func(paths []string) (core.ConfigurationModel, error) {
			loaded = append([]string{}, paths...)
			return core.ConfigurationModelFromFlatMap(map[string]string{
				"environments.default.url": "postgres://u:p@localhost:5432/toml_db",
			})
		}
		inv, code, stop := resolveInvocation([]string{
			"migrate", "-workingDirectory", work, "-configFiles", "config.toml",
		}, io.Discard, io.Discard, deps)
		if stop || code != 0 {
			t.Fatalf("stop=%v code=%d", stop, code)
		}
		if inv.Config.Mode != core.ConfigModeModern {
			t.Fatalf("mode = %v, want Modern", inv.Config.Mode)
		}
		if strings.Join(loaded, "|") != tomlPath {
			t.Fatalf("loaded = %#v, want [%s]", loaded, tomlPath)
		}
		if inv.Config.URL != "postgres://u:p@localhost:5432/toml_db" {
			t.Fatalf("url = %q", inv.Config.URL)
		}
	})

	for _, fileName := range []string{"config.conf", "config.TOML"} {
		t.Run(fileName+" loads through Classic", func(t *testing.T) {
			configPath := core.AbsoluteFilePath(filepath.Join(work, fileName))
			var loaded []string
			deps := modernResolverDependencies(work)
			deps.fileExists = func(path string) bool { return path == configPath }
			deps.loadDefaultConfig = func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
				return map[string]string{"flyway.url": "postgres://u:p@localhost:5432/default_db"}, nil
			}
			deps.loadConfigFile = func(path, encoding string, failIfMissing bool) (map[string]string, error) {
				loaded = append(loaded, path)
				return map[string]string{}, nil
			}
			inv, code, stop := resolveInvocation([]string{
				"migrate", "-workingDirectory", work, "-configFiles", fileName,
			}, io.Discard, io.Discard, deps)
			if stop || code != 0 {
				t.Fatalf("stop=%v code=%d", stop, code)
			}
			if inv.Config.Mode != core.ConfigModeClassic {
				t.Fatalf("mode = %v, want Classic", inv.Config.Mode)
			}
			if strings.Join(loaded, "|") != configPath {
				t.Fatalf("loaded = %#v, want [%s]", loaded, configPath)
			}
		})
	}
}

func TestResolveInvocationRejectsUnknownModernConfigurationKey(t *testing.T) {
	deps := modernResolverDependencies(t.TempDir())
	var stderr bytes.Buffer
	_, code, stop := resolveInvocation([]string{
		"migrate",
		"-url=postgres://u:p@localhost:5432/db",
		"-notARealSetting=value",
	}, io.Discard, &stderr, deps)
	if !stop || code != 1 {
		t.Fatalf("stop=%v code=%d, want true/1", stop, code)
	}
	if !strings.Contains(stderr.String(), "Unknown configuration property: flyway.notARealSetting") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
