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

func TestValidateExitCode(t *testing.T) {
	if got := validateExitCode(core.ValidateResult{Valid: true}); got != 0 {
		t.Fatalf("valid validation exit code = %d, want 0", got)
	}
	if got := validateExitCode(core.ValidateResult{Valid: false}); got != 1 {
		t.Fatalf("invalid validation exit code = %d, want 1", got)
	}
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		driverHint string
		wantDriver string
		wantDSN    string
		wantErr    bool
	}{
		{
			name:       "postgres 短 URL",
			url:        "postgres://u:p@localhost:5432/app?sslmode=disable",
			wantDriver: "postgres",
			wantDSN:    "postgres://u:p@localhost:5432/app?sslmode=disable",
		},
		{
			name:       "postgresql 别名",
			url:        "postgresql://u:p@localhost:5432/app?sslmode=disable",
			wantDriver: "postgres",
			wantDSN:    "postgres://u:p@localhost:5432/app?sslmode=disable",
		},
		{
			name:       "jdbc 前缀被剥离",
			url:        "jdbc:postgresql://localhost:5432/app",
			wantDriver: "postgres",
			wantDSN:    "postgres://localhost:5432/app",
		},
		{
			name:       "mysql 带端口",
			url:        "mysql://root:pass@tcp(127.0.0.1:3306)/app?parseTime=true",
			wantDriver: "mysql",
			wantDSN:    "root:pass@tcp(127.0.0.1:3306)/app?parseTime=true",
		},
		{
			name:       "mysql 主机端口写法",
			url:        "mysql://root:pass@127.0.0.1:3307/app",
			wantDriver: "mysql",
			wantDSN:    "root:pass@tcp(127.0.0.1:3307)/app",
		},
		{
			name:       "mysql 省略端口默认 3306",
			url:        "mysql://root:pass@localhost/app",
			wantDriver: "mysql",
			wantDSN:    "root:pass@tcp(localhost:3306)/app",
		},
		{
			name:       "mysql 密码含特殊字符",
			url:        "mysql://root:p@ss@localhost/db",
			wantDriver: "mysql",
			wantDSN:    "root:p@ss@tcp(localhost:3306)/db",
		},
		{
			name:       "无前缀靠 driver 提示",
			url:        "host=localhost user=u",
			driverHint: "postgres",
			wantDriver: "postgres",
			wantDSN:    "host=localhost user=u",
		},
		{
			name:    "无法推断驱动",
			url:     "host=localhost",
			wantErr: true,
		},
		{
			name:    "mysql 缺少数据库名",
			url:     "mysql://root:pass@localhost",
			wantErr: true,
		},
		{
			name:    "mysql 缺少 @",
			url:     "mysql://localhost/db",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			drv, dsn, err := parseURL(c.url, c.driverHint)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际 driver=%s dsn=%s", drv, dsn)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if drv != c.wantDriver {
				t.Errorf("driver = %s, 期望 %s", drv, c.wantDriver)
			}
			if dsn != c.wantDSN {
				t.Errorf("dsn = %s, 期望 %s", dsn, c.wantDSN)
			}
		})
	}
}
func TestConfigDefaultsAndTargetSemantics(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		want     string
		wantFail bool
	}{
		{name: "empty", target: "", want: "latest", wantFail: true},
		{name: "plain", target: "1", want: "1", wantFail: true},
		{name: "optional", target: "1?", want: "1", wantFail: false},
		{name: "latest", target: "latest", want: "latest", wantFail: true},
		{name: "latest optional", target: "latest?", want: "latest", wantFail: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := core.Config{TargetVersion: c.target}
			cfg.ApplyDefaults()
			// The CLI applies defaults before core.New, which applies them once
			// more. Target normalization therefore has to be idempotent.
			cfg.ApplyDefaults()
			if cfg.TargetVersion != c.want || cfg.FailOnMissingTarget != c.wantFail {
				t.Fatalf("target = (%q,%v), want (%q,%v)", cfg.TargetVersion, cfg.FailOnMissingTarget, c.want, c.wantFail)
			}
			if len(cfg.Locations) != 1 || cfg.Locations[0] != "db/migration" {
				t.Fatalf("locations = %#v, want [db/migration]", cfg.Locations)
			}
			if cfg.CleanDisabled == nil || !*cfg.CleanDisabled {
				t.Fatalf("cleanDisabled = %#v, want true", cfg.CleanDisabled)
			}
			if cfg.ValidateOnMigrate == nil || !*cfg.ValidateOnMigrate {
				t.Fatalf("validateOnMigrate = %#v, want true", cfg.ValidateOnMigrate)
			}
		})
	}
}

func TestParsePlaceholderAssignments(t *testing.T) {
	got := parsePlaceholderAssignments("schema=app, token=a=b, empty=")
	want := map[string]string{"schema": "app", "token": "a=b", "empty": ""}
	if len(got) != len(want) {
		t.Fatalf("placeholders = %#v, want %#v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("placeholder %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestFlagPropertyNameBaselineMigrationPrefix(t *testing.T) {
	if got := flagPropertyName("baseline-migration-prefix"); got != "flyway.baselineMigrationPrefix" {
		t.Fatalf("property name = %q", got)
	}
}

func TestResolveInvocationConfigurationPrecedence(t *testing.T) {
	deps := cliDependencies{
		loadDefaultConfig: func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
			return map[string]string{
				"flyway.url":       "postgres://file:file@localhost:5432/file_db",
				"flyway.locations": "from-file",
				"flyway.user":      "file-user",
			}, nil
		},
		environment: func() map[string]string {
			return map[string]string{
				"flyway.url":       "postgres://env:env@localhost:5432/env_db",
				"flyway.locations": "from-env",
				"flyway.password":  "env-password",
			}
		},
	}
	inv, code, stop := resolveInvocation([]string{
		"migrate",
		"-url", "postgres://cli:cli@localhost:5432/cli_db",
		"-locations", "from-cli",
		"-placeholders", "schema=cli, token=a=b",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	if inv.Config.URL != "postgres://cli:cli@localhost:5432/cli_db" {
		t.Fatalf("url = %q", inv.Config.URL)
	}
	// URL comes from the explicit CLI flag, while user/password remain separate
	// configuration properties and therefore still come from the environment.
	if inv.Driver != "postgres" || inv.DSN != "postgres://file-user:env-password@localhost:5432/cli_db" {
		t.Fatalf("driver/dsn = %q/%q", inv.Driver, inv.DSN)
	}
	if len(inv.Config.Locations) != 1 || inv.Config.Locations[0] != "from-cli" {
		t.Fatalf("locations = %#v", inv.Config.Locations)
	}
	if inv.Config.Placeholders["schema"] != "cli" || inv.Config.Placeholders["token"] != "a=b" {
		t.Fatalf("placeholders = %#v", inv.Config.Placeholders)
	}
}

func TestResolveInvocationConfigFilesOrderAndEncoding(t *testing.T) {
	var loaded []string
	var encodings []string
	deps := cliDependencies{
		loadDefaultConfig: func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
			loaded = append(loaded, "defaults")
			encodings = append(encodings, encoding)
			return map[string]string{
				"flyway.url":   "postgres://defaults:defaults@localhost:5432/defaults",
				"flyway.table": "defaults_table",
			}, nil
		},
		loadConfigFile: func(path, encoding string, failIfMissing bool) (map[string]string, error) {
			loaded = append(loaded, path)
			encodings = append(encodings, encoding)
			if !failIfMissing {
				t.Fatalf("explicit config file %q was not marked failIfMissing", path)
			}
			switch filepath.Base(path) {
			case "env.conf":
				return map[string]string{"flyway.table": "env_table", "flyway.user": "env_user"}, nil
			case "cli.conf":
				return map[string]string{"flyway.table": "cli_table", "flyway.locations": "cli_locations"}, nil
			}
			return nil, fmt.Errorf("unexpected file %s", path)
		},
		environment: func() map[string]string {
			return map[string]string{
				"flyway.configFiles":        "env.conf",
				"flyway.configFileEncoding": "UTF-16",
				"flyway.url":                "postgres://env:env@localhost:5432/env",
				"flyway.locations":          "env_locations",
			}
		},
	}
	work := t.TempDir()
	inv, code, stop := resolveInvocation([]string{
		"migrate",
		"-workingDirectory", work,
		"-configFiles", "cli.conf",
		"-configFileEncoding", "GBK",
		"-table", "cli_table_override",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	wantLoaded := []string{"defaults", filepath.Join(work, "env.conf"), filepath.Join(work, "cli.conf")}
	if strings.Join(loaded, "|") != strings.Join(wantLoaded, "|") {
		t.Fatalf("loaded = %#v, want %#v", loaded, wantLoaded)
	}
	if len(encodings) != 3 || encodings[0] != "UTF-16" || encodings[1] != "UTF-16" || encodings[2] != "UTF-16" {
		t.Fatalf("encodings = %#v, want environment encoding to win", encodings)
	}
	if inv.Config.Table != "cli_table_override" || len(inv.Config.Locations) != 1 || inv.Config.Locations[0] != "env_locations" {
		t.Fatalf("configuration = %#v", inv.Config)
	}
	if inv.Config.User != "env_user" || inv.Config.URL != "postgres://env:env@localhost:5432/env" {
		t.Fatalf("configuration precedence = %#v", inv.Config)
	}
}

func TestResolveInvocationCLIEncodingWinsOverUTF8(t *testing.T) {
	var gotEncoding string
	deps := cliDependencies{
		loadDefaultConfig: func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
			gotEncoding = encoding
			return map[string]string{"flyway.url": "postgres://u:p@localhost:5432/db"}, nil
		},
		loadConfigFile: func(path, encoding string, failIfMissing bool) (map[string]string, error) {
			gotEncoding = encoding
			return map[string]string{"flyway.locations": "migrations"}, nil
		},
		environment: func() map[string]string { return map[string]string{"flyway.configFiles": "extra.conf"} },
	}
	_, code, stop := resolveInvocation([]string{
		"migrate", "-configFiles", "explicit.conf", "-configFileEncoding", "UTF-16",
	}, io.Discard, io.Discard, deps)
	if stop || code != 0 {
		t.Fatalf("stop=%v code=%d", stop, code)
	}
	if gotEncoding != "UTF-16" {
		t.Fatalf("encoding = %q, want UTF-16", gotEncoding)
	}
}

func TestResolveInvocationExplicitConfigFileErrorsStop(t *testing.T) {
	want := fmt.Errorf("Unable to load config file: missing.conf")
	deps := cliDependencies{
		loadDefaultConfig: func(installationDirectory, workingDirectory, encoding string) (map[string]string, error) {
			return map[string]string{}, nil
		},
		loadConfigFile: func(path, encoding string, failIfMissing bool) (map[string]string, error) {
			return nil, want
		},
		environment: func() map[string]string { return map[string]string{} },
	}
	var stderr bytes.Buffer
	_, code, stop := resolveInvocation([]string{"migrate", "-configFiles", "missing.conf"}, io.Discard, &stderr, deps)
	if !stop || code != 1 {
		t.Fatalf("stop=%v code=%d, want true/1", stop, code)
	}
	if !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestResolveInvocationURLUserPasswordOverrides(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		user       string
		password   string
		wantDriver string
		wantDSN    string
	}{
		{
			name:       "postgres",
			url:        "postgres://old:old@localhost:5432/app?sslmode=disable",
			user:       "new",
			password:   "secret",
			wantDriver: "postgres",
			wantDSN:    "postgres://new:secret@localhost:5432/app?sslmode=disable",
		},
		{
			name:       "mysql URL",
			url:        "mysql://old:old@tcp(localhost:3306)/app?parseTime=true",
			user:       "new",
			password:   "secret",
			wantDriver: "mysql",
			wantDSN:    "new:secret@tcp(localhost:3306)/app?parseTime=true",
		},
		{
			name:       "mysql DSN",
			url:        "old:old@tcp(localhost:3306)/app",
			user:       "new",
			password:   "secret",
			wantDriver: "mysql",
			wantDSN:    "new:secret@tcp(localhost:3306)/app",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := []string{"migrate", "-url", c.url, "-user", c.user, "-password", c.password}
			if c.wantDriver == "mysql" && !strings.HasPrefix(c.url, "mysql://") {
				args = append(args, "-driver", "mysql")
			}
			inv, code, stop := resolveInvocation(args, io.Discard, io.Discard, cliDependencies{})
			if stop || code != 0 {
				t.Fatalf("stop=%v code=%d", stop, code)
			}
			if inv.Driver != c.wantDriver || inv.DSN != c.wantDSN {
				t.Fatalf("driver/dsn = %q/%q, want %q/%q", inv.Driver, inv.DSN, c.wantDriver, c.wantDSN)
			}
		})
	}
}

func TestResolveInvocationFailsOnMissingURLWithoutUserConfig(t *testing.T) {
	var stderr bytes.Buffer
	_, code, stop := resolveInvocation([]string{"migrate"}, io.Discard, &stderr, cliDependencies{})
	if !stop || code != 2 {
		t.Fatalf("stop=%v code=%d, want true/2", stop, code)
	}
	if !strings.Contains(stderr.String(), "必须通过 -url") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunUndoIsNotAnOSSCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"undo"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "未知命令") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "用法") {
		t.Fatalf("stderr should contain usage, got %q", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}, {"migrate", "-h"}, {"migrate", "--help"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "用法") {
				t.Fatalf("stdout should contain usage, got %q", stdout.String())
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"deploy"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "未知命令") {
		t.Fatalf("stderr = %q, want unknown command", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "-does-not-exist"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want flag parse error", stderr.String())
	}
}

func TestRunMissingURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "必须通过 -url") {
		t.Fatalf("stderr = %q, want missing url", stderr.String())
	}
}

func TestRunInvalidBool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "-url", "postgres://u:p@localhost/db", "-validate-on-migrate=maybe"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "invalid boolean value") {
		t.Fatalf("stderr = %q, want bool parse error", stderr.String())
	}
}

func TestRunUnknownPositionalArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "-url", "postgres://u:p@localhost/db", "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "未知参数") {
		t.Fatalf("stderr = %q, want positional argument error", stderr.String())
	}
}

func TestPrintInfoPreservesEmptySyntheticCategory(t *testing.T) {
	var out bytes.Buffer
	printInfo([]core.InfoEntry{{
		Category:      "",
		Version:       "1",
		Description:   "<< Flyway Baseline >>",
		Type:          core.TypeBaseline,
		State:         string(core.StateBaseline),
		InstalledRank: intPtr(1),
		InstalledBy:   "postgres",
	}}, &out)
	want := "INFO\t\t1\t<< Flyway Baseline >>\tBASELINE\tBASELINE\t\tNULL\t1\tpostgres\tNULL\tNULL\n"
	if got := out.String(); got != want {
		t.Fatalf("printInfo() = %q, want %q", got, want)
	}
}

func intPtr(v int) *int { return &v }
