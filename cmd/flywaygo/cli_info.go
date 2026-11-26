package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/linsu/flywaygo/core"
)

const (
	flywayOSSVersion = "13.13.0"
	infoDateHelpURL  = "https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=reference/usage/command-line"
)

var infoDatePattern = regexp.MustCompile(`^(\d+)/(\d+)/(\d+) (\d+):(\d+)$`)
var jdbcURLPrefixPattern = regexp.MustCompile(`^(jdbc:[^:]+:).*`)

func (r *userCLIRuntime) executeInfo(ctx context.Context, fw *core.Flyway, parsed userCLIArgs, cfg core.Config) int {
	snapshot, err := fw.InfoSnapshot(ctx)
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	unfiltered := snapshot.Result(nil)
	r.printInfoHeader(cfg, unfiltered)
	filter, err := userCLIInfoFilter(parsed.configuration)
	if err != nil {
		r.printRuntimeError(err.Error())
		return 1
	}
	filtered := snapshot.Result(filter)
	if parsed.migrationIDs {
		_, _ = io.WriteString(r.stdout, core.DumpInfoMigrationIDs(filtered.Entries))
	} else {
		r.printInfoTables(cfg, filtered.Entries)
	}
	if !r.outputJSON {
		return 0
	}
	payload := []byte(renderInfoJSON(filtered, time.Now()))
	if parsed.migrationIDs {
		_, _ = r.stdout.Write(payload)
		if r.outputFile != "" {
			_ = os.WriteFile(r.outputFile, stripFinalCRLF(payload), 0o644)
		}
		return 0
	}
	return r.writeJSONResult(payload)
}

func (r *userCLIRuntime) printInfoHeader(cfg core.Config, result core.InfoResult) {
	product := strings.TrimSpace(result.DatabaseProductName + " " + result.DatabaseProductVersion)
	r.logger.Info("Environment: %s", describeCurrentEnvironment(cfg))
	r.logger.Info("Database: %s (%s)", redactJdbcURL(cfg.URL), product)
	if !result.HistoryExists {
		r.logger.Info("Schema history table %s does not exist yet", result.HistoryTableName)
	}
	r.logger.Info("Schema version: %s", result.SchemaVersion)
	r.logger.Info("")
}

func (r *userCLIRuntime) printInfoTables(cfg core.Config, entries []core.InfoEntry) {
	if !infoEntriesHavePhysicalLocation(entries) {
		r.logger.Info("%s", "No migrations found on disk.\nHere are some relevant configuration settings.")
		r.logger.Info("%s", "\n"+core.DumpMigrationSettingsTable(cfg))
	}
	r.logger.Info("%s", core.DumpInfoAsciiTable(entries))
}

func infoEntriesHavePhysicalLocation(entries []core.InfoEntry) bool {
	for _, entry := range entries {
		if entry.PhysicalLocation != "" {
			return true
		}
	}
	return false
}

func describeCurrentEnvironment(cfg core.Config) string {
	name := cfg.EnvironmentName
	if name == "" {
		name = "default"
	}
	if strings.TrimSpace(cfg.DisplayName) != "" {
		return name + " (" + cfg.DisplayName + ")"
	}
	return name
}

func redactJdbcURL(url string) string {
	if strings.EqualFold(os.Getenv("FLYWAY_DO_NOT_REDACT_URL"), "true") {
		return url
	}
	if match := jdbcURLPrefixPattern.FindStringSubmatch(url); match != nil {
		return match[1] + "********"
	}
	return "********"
}

func userCLIInfoFilter(configuration map[string]string) (*core.InfoFilter, error) {
	sinceDate, err := parseInfoDate(configuration["infoSinceDate"], "infoSinceDate")
	if err != nil {
		return nil, err
	}
	untilDate, err := parseInfoDate(configuration["infoUntilDate"], "infoUntilDate")
	if err != nil {
		return nil, err
	}
	sinceVersion, err := parseInfoVersion(configuration["infoSinceVersion"])
	if err != nil {
		return nil, err
	}
	untilVersion, err := parseInfoVersion(configuration["infoUntilVersion"])
	if err != nil {
		return nil, err
	}
	states, err := parseInfoStates(configuration["infoOfState"])
	if err != nil {
		return nil, err
	}
	if sinceDate == nil && untilDate == nil && sinceVersion == nil && untilVersion == nil && len(states) == 0 {
		return nil, nil
	}
	return &core.InfoFilter{
		SinceDate:    sinceDate,
		UntilDate:    untilDate,
		SinceVersion: sinceVersion,
		UntilVersion: untilVersion,
		States:       states,
	}, nil
}

func parseInfoDate(value, name string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	match := infoDatePattern.FindStringSubmatch(value)
	if match == nil {
		return nil, fmt.Errorf("'%s' is an invalid value for the %s option. The expected format is 'dd/mm/yyyy hh:mm', like '13/10/2020 16:30'. See the Flyway documentation for help: %s", value, name, infoDateHelpURL)
	}
	numbers := make([]int, 5)
	for i := 0; i < 5; i++ {
		number, err := strconv.Atoi(match[i+1])
		if err != nil {
			return nil, fmt.Errorf("'%s' is an invalid value for the %s option. The expected format is 'dd/mm/yyyy hh:mm', like '13/10/2020 16:30'. See the Flyway documentation for help: %s", value, name, infoDateHelpURL)
		}
		numbers[i] = number
	}
	// SimpleDateFormat is lenient: overflow rolls into the next field.
	parsed := time.Date(numbers[2], time.Month(numbers[1]), numbers[0], numbers[3], numbers[4], 0, 0, time.Local)
	return &parsed, nil
}

func parseInfoVersion(value string) (*core.Version, error) {
	if value == "" {
		return nil, nil
	}
	version, err := core.ParseVersion(value)
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func parseInfoStates(value string) ([]core.MigrationState, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	states := make([]core.MigrationState, 0, len(parts))
	for _, part := range parts {
		name := strings.ToUpper(part)
		state, ok := infoStateByName(name)
		if !ok {
			return nil, fmt.Errorf("java.lang.IllegalArgumentException: No enum constant org.flywaydb.core.api.MigrationState.%s", name)
		}
		states = append(states, state)
	}
	return states, nil
}

func infoStateByName(name string) (core.MigrationState, bool) {
	switch name {
	case "PENDING":
		return core.StatePending, true
	case "ABOVE_TARGET":
		return core.StateAboveTarget, true
	case "BELOW_BASELINE":
		return core.StateBelowBaseline, true
	case "BASELINE_IGNORED":
		return core.StateBaselineIgnored, true
	case "BASELINE":
		return core.StateBaseline, true
	case "IGNORED":
		return core.StateIgnored, true
	case "MISSING_SUCCESS":
		return core.StateMissingSuccess, true
	case "MISSING_FAILED":
		return core.StateMissingFailed, true
	case "SUCCESS":
		return core.StateSuccess, true
	case "UNDONE":
		return core.StateUndone, true
	case "AVAILABLE":
		return core.StateAvailable, true
	case "FAILED":
		return core.StateFailed, true
	case "OUT_OF_ORDER":
		return core.StateOutOfOrder, true
	case "FUTURE_SUCCESS":
		return core.StateFutureSuccess, true
	case "FUTURE_FAILED":
		return core.StateFutureFailed, true
	case "OUTDATED":
		return core.StateOutdated, true
	case "SUPERSEDED":
		return core.StateSuperseded, true
	case "DELETED":
		return core.StateDeleted, true
	default:
		return "", false
	}
}

func renderInfoJSON(result core.InfoResult, timestamp time.Time) string {
	var b strings.Builder
	b.WriteString("{\r\n")
	writeJSONField(&b, 1, "allSchemasEmpty", jsonBool(result.AllSchemasEmpty), true)
	writeJSONField(&b, 1, "database", quoteJSONString(result.DatabaseName), true)
	writeJSONField(&b, 1, "environment", quoteJSONString(result.EnvironmentName), true)
	writeJSONField(&b, 1, "exception", "null", true)
	writeJSONField(&b, 1, "flywayVersion", quoteJSONString(flywayOSSVersion), true)
	writeJSONField(&b, 1, "licenseFailed", "false", true)
	b.WriteString("  \"migrations\" : ")
	writeInfoMigrations(&b, result.Entries)
	b.WriteString(",\r\n")
	writeJSONField(&b, 1, "operation", quoteJSONString("info"), true)
	writeJSONField(&b, 1, "schemaName", quoteJSONString(result.SchemaName), true)
	writeJSONField(&b, 1, "schemaVersion", quoteJSONString(result.SchemaVersion), true)
	writeJSONField(&b, 1, "timestamp", quoteJSONString(formatInfoTimestamp(timestamp)), false)
	b.WriteString("}\r\n")
	return b.String()
}

func writeInfoMigrations(b *strings.Builder, entries []core.InfoEntry) {
	visible := make([]core.InfoEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.State == string(core.StateAvailable) {
			continue
		}
		visible = append(visible, entry)
	}
	if len(visible) == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteString("[ ")
	for i, entry := range visible {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{\r\n")
		writeJSONField(b, 2, "category", quoteJSONString(entry.Category), true)
		writeJSONField(b, 2, "description", quoteJSONString(entry.Description), true)
		writeJSONField(b, 2, "executionTime", strconv.Itoa(entry.ExecutionTime), true)
		writeJSONField(b, 2, "filepath", quoteJSONString(entry.PhysicalLocation), true)
		writeJSONField(b, 2, "installedBy", quoteJSONString(entry.InstalledBy), true)
		writeJSONField(b, 2, "installedOnUTC", quoteJSONString(entry.InstalledOnUTC), true)
		writeJSONField(b, 2, "rawVersion", quoteJSONString(entry.RawVersion), true)
		writeJSONField(b, 2, "shouldExecuteExpression", "null", true)
		writeJSONField(b, 2, "state", quoteJSONString(entry.StateDisplay), true)
		writeJSONField(b, 2, "type", quoteJSONString(entry.Type), true)
		writeJSONField(b, 2, "undoFilepath", quoteJSONString(entry.UndoFilepath), true)
		writeJSONField(b, 2, "undoable", quoteJSONString(entry.UndoableJSON), true)
		writeJSONField(b, 2, "version", quoteJSONString(infoJSONVersion(entry)), false)
		b.WriteString("  }")
	}
	b.WriteString(" ]")
}

func infoJSONVersion(entry core.InfoEntry) string {
	if entry.VersionNull {
		return ""
	}
	if entry.VersionForID != "" {
		return entry.VersionForID
	}
	if entry.Version == "null" {
		return ""
	}
	return entry.Version
}

func writeJSONField(b *strings.Builder, indent int, key, value string, comma bool) {
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteString(quoteJSONString(key))
	b.WriteString(" : ")
	b.WriteString(value)
	if comma {
		b.WriteString(",")
	}
	b.WriteString("\r\n")
}

func jsonBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatInfoTimestamp(timestamp time.Time) string {
	return timestamp.In(time.Local).Format(time.RFC3339Nano)
}
