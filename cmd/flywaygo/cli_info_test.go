package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/linsu/flywaygo/core"
)

func TestRedactJdbcURL(t *testing.T) {
	got := redactJdbcURL("jdbc:postgresql://localhost:5432/flywaygo")
	if got != "jdbc:postgresql:********" {
		t.Fatalf("redacted = %q", got)
	}
	if got := redactJdbcURL("not-a-jdbc-url"); got != "********" {
		t.Fatalf("unknown url = %q", got)
	}
	t.Setenv("FLYWAY_DO_NOT_REDACT_URL", "true")
	if got := redactJdbcURL("jdbc:postgresql://localhost/flywaygo"); got != "jdbc:postgresql://localhost/flywaygo" {
		t.Fatalf("unredacted = %q", got)
	}
}

func TestParseInfoDateLenient(t *testing.T) {
	parsed, err := parseInfoDate("31/02/2020 10:00", "infoSinceDate")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2020, 3, 2, 10, 0, 0, 0, time.Local)
	if !parsed.Equal(want) {
		t.Fatalf("parsed = %s, want %s", parsed, want)
	}
	if _, err := parseInfoDate("bad", "infoSinceDate"); err == nil || !strings.Contains(err.Error(), "invalid value for the infoSinceDate option") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseInfoFilterErrors(t *testing.T) {
	_, err := userCLIInfoFilter(map[string]string{"infoOfState": "bogus"})
	if err == nil || err.Error() != "java.lang.IllegalArgumentException: No enum constant org.flywaydb.core.api.MigrationState.BOGUS" {
		t.Fatalf("state error = %v", err)
	}
	_, err = userCLIInfoFilter(map[string]string{"infoSinceVersion": "bad"})
	if err == nil || err.Error() != "Version may only contain 0..9 and . (dot). Invalid version: bad" {
		t.Fatalf("version error = %v", err)
	}
	filter, err := userCLIInfoFilter(map[string]string{"infoOfState": "success,pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filter.States) != 2 || filter.States[0] != core.StateSuccess || filter.States[1] != core.StatePending {
		t.Fatalf("states = %#v", filter.States)
	}
}

func TestRenderInfoJSONShape(t *testing.T) {
	result := core.InfoResult{
		SchemaVersion:   "2",
		DatabaseName:    "flywaygo",
		EnvironmentName: "default",
		Entries: []core.InfoEntry{
			{Category: "Versioned", Version: "1", VersionForID: "1", RawVersion: "1", Description: "first", Type: "SQL", State: "SUCCESS", StateDisplay: "Success", InstalledOnUTC: "2026-11-22T21:40:49.707545Z", InstalledBy: "postgres", PhysicalLocation: `E:\sql\V1__first.sql`, ExecutionTime: 6, UndoableJSON: "No"},
			{Category: "Versioned", State: string(core.StateAvailable), StateDisplay: "Available", Description: "hidden"},
		},
	}
	timestamp := time.Date(2026, 11, 23, 5, 41, 4, 257903300, time.FixedZone("CST", 8*3600))
	got := renderInfoJSON(result, timestamp)
	if strings.Contains(got, "hidden") || strings.Contains(got, "AVAILABLE") {
		t.Fatalf("available migration leaked: %s", got)
	}
	if !strings.Contains(got, "1,") && !strings.HasPrefix(got, "{") {
		t.Fatal(got)
	}
	wantPrefix := "{\r\n  \"allSchemasEmpty\" : false,\r\n  \"database\" : \"flywaygo\",\r\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("prefix = %q", got[:80])
	}
	if !strings.Contains(got, "\"shouldExecuteExpression\" : null") || !strings.Contains(got, "\"exception\" : null") {
		t.Fatalf("null fields missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "\"timestamp\" : \"2026-11-23T05:41:04.2579033+08:00\"\r\n}\r\n") {
		t.Fatalf("suffix = %q", got[len(got)-80:])
	}
}

func TestMigrationIDsLoggerIsSilent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := &userCLILogger{stdout: &stdout, stderr: &stderr, silent: true, debug: true}
	logger.Info("Environment: default")
	logger.Error("%s", "boom")
	logger.Debug("%s", "hidden")
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("silent logger wrote stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestDumpMigrationSettingsTableOrder(t *testing.T) {
	table := core.DumpMigrationSettingsTable(core.Config{
		Locations:        []string{"filesystem:sql"},
		WorkingDirectory: `E:\work\probe`,
		Naming:           core.DefaultNaming(),
	})
	if !strings.Contains(table, "No settings found") && !strings.Contains(table, "| locations") {
		t.Fatalf("table = %s", table)
	}
	locations := strings.Index(table, "locations")
	callbacks := strings.Index(table, "callbackLocations")
	suffixes := strings.Index(table, "sqlMigrationSuffixes")
	if locations < 0 || callbacks < locations || suffixes < callbacks {
		t.Fatalf("row order:\n%s", table)
	}
}
