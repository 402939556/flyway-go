package core

import (
	"os"
	"strings"
	"unicode/utf16"
)

var infoTableColumns = []string{
	"Category",
	"Version",
	"Description",
	"Type",
	"Installed On",
	"State",
	"Undoable",
}

// DumpInfoAsciiTable mirrors MigrationInfoDumper.dumpToAsciiTable().
// The per-row category, state and undoable fields are computed once by
// infoEntryFor, exactly like MigrationInfoImpl. Undoable is already resolved from the filtered info collection passed to the dumper.
func DumpInfoAsciiTable(entries []InfoEntry) string {
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		if isUndoType(entry.Type) {
			continue
		}
		rows = append(rows, []string{
			entry.Category,
			infoTableVersion(entry),
			entry.Description,
			entry.Type,
			entry.InstalledOnDisplay,
			entry.StateDisplay,
			entry.Undoable,
		})
	}
	return renderAsciiTable(infoTableColumns, rows, true, "", "No migrations found")
}

// DumpInfoMigrationIDs mirrors MigrationInfoDumper.dumpToMigrationIds().
func DumpInfoMigrationIDs(entries []InfoEntry) string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if isUndoType(entry.Type) {
			continue
		}
		if entry.VersionNull || entry.Version == "" || entry.Version == "null" {
			ids = append(ids, entry.Description)
			continue
		}
		if entry.VersionForID != "" {
			ids = append(ids, entry.VersionForID)
			continue
		}
		ids = append(ids, entry.Version)
	}
	return strings.Join(ids, ",")
}

func infoTableVersion(entry InfoEntry) string {
	if entry.VersionNull || entry.Version == "" || entry.Version == "null" {
		return ""
	}
	return entry.Version
}

func renderAsciiTable(columns []string, rows [][]string, printHeader bool, nullText, emptyText string) string {
	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = javaStringLength(column)
	}
	getValue := func(row []string, i int) string {
		value := nullText
		if i < len(row) {
			value = row[i]
		}
		return value
	}
	for _, row := range rows {
		for i := range widths {
			if width := javaStringLength(getValue(row, i)); width > widths[i] {
				widths[i] = width
			}
		}
	}
	var ruler strings.Builder
	ruler.WriteByte('+')
	for _, width := range widths {
		ruler.WriteString("-")
		ruler.WriteString(strings.Repeat("-", width))
		ruler.WriteString("-+")
	}
	ruler.WriteString("\n")
	rulerText := ruler.String()
	var out strings.Builder
	if printHeader {
		out.WriteString(rulerText)
		out.WriteString("|")
		for i, width := range widths {
			out.WriteString(" ")
			out.WriteString(trimOrPad(columns[i], width, ' '))
			out.WriteString(" |")
		}
		out.WriteString("\n")
	}
	out.WriteString(rulerText)
	if len(rows) == 0 {
		emptyWidth := javaStringLength(rulerText) - minInt(javaStringLength(rulerText), 5)
		out.WriteString("| ")
		out.WriteString(trimOrPad(emptyText, emptyWidth, ' '))
		out.WriteString(" |\n")
	} else {
		for _, row := range rows {
			out.WriteString("|")
			for i, width := range widths {
				out.WriteString(" ")
				out.WriteString(trimOrPad(getValue(row, i), width, ' '))
				out.WriteString(" |")
			}
			out.WriteString("\n")
		}
	}
	out.WriteString(rulerText)
	return out.String()
}

func trimOrPad(value string, width int, pad rune) string {
	units := utf16.Encode([]rune(value))
	if len(units) > width {
		units = units[:width]
	}
	for len(units) < width {
		units = append(units, uint16(pad))
	}
	return string(utf16.Decode(units))
}

func javaStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// DumpMigrationSettingsTable mirrors MigrationConfigPrinter when no filtered
// migration has a physical location. Location descriptors keep an explicit
// prefix and otherwise use filesystem:, matching Location.getDescriptor().
func DumpMigrationSettingsTable(cfg Config) string {
	workingDirectory := cfg.WorkingDirectory
	if workingDirectory == "" {
		if dir, err := os.Getwd(); err == nil {
			workingDirectory = dir
		}
	}
	rows := [][]string{
		{"locations", formatLocationDescriptors(cfg.Locations)},
		{"callbackLocations", formatLocationDescriptors(cfg.CallbackLocations)},
		{"workingDirectory", workingDirectory},
		{"repeatableSqlMigrationPrefix", cfg.Naming.RepeatablePrefix},
		{"sqlMigrationPrefix", cfg.Naming.VersionedPrefix},
		{"baselineMigrationPrefix", cfg.Naming.BaselinePrefix},
		{"sqlMigrationSeparator", cfg.Naming.Separator},
		{"sqlMigrationSuffixes", strings.Join(cfg.Naming.Suffixes, ", ")},
	}
	return renderAsciiTable([]string{"Setting", "Value"}, rows, true, "", "No settings found")
}

func formatLocationDescriptors(locations []string) string {
	described := make([]string, 0, len(locations))
	for _, location := range locations {
		described = append(described, formatLocationDescriptor(location))
	}
	return strings.Join(described, ", ")
}

func formatLocationDescriptor(location string) string {
	if strings.Contains(location, ":") {
		return location
	}
	return "filesystem:" + location
}
