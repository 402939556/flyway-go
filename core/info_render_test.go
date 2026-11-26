package core

import (
	"strings"
	"testing"
)

func TestDumpInfoAsciiTableUsesComputedEntryFields(t *testing.T) {
	entries := []InfoEntry{
		{
			Category:           "",
			Version:            "1",
			Description:        "init",
			Type:               TypeSQLMigration,
			InstalledOnDisplay: "2020-10-13 16:30:00",
			StateDisplay:       "Success",
			Undoable:           "Yes",
		},
		{
			Category:     "Repeatable",
			Version:      "null",
			VersionNull:  true,
			Description:  "seed",
			Type:         TypeSQLMigration,
			StateDisplay: "Pending",
			Undoable:     "No",
		},
		{
			Category: "Undo",
			Version:  "1",
			Type:     TypeSQLUndo,
		},
	}

	want := strings.Join([]string{
		"+------------+---------+-------------+------+---------------------+---------+----------+",
		"| Category   | Version | Description | Type | Installed On        | State   | Undoable |",
		"+------------+---------+-------------+------+---------------------+---------+----------+",
		"|            | 1       | init        | SQL  | 2020-10-13 16:30:00 | Success | Yes      |",
		"| Repeatable |         | seed        | SQL  |                     | Pending | No       |",
		"+------------+---------+-------------+------+---------------------+---------+----------+",
		"",
	}, "\n")

	if got := DumpInfoAsciiTable(entries); got != want {
		t.Fatalf("DumpInfoAsciiTable() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDumpInfoAsciiTableEmpty(t *testing.T) {
	want := "+----------+---------+-------------+------+--------------+-------+----------+\n" +
		"| Category | Version | Description | Type | Installed On | State | Undoable |\n" +
		"+----------+---------+-------------+------+--------------+-------+----------+\n" +
		"| No migrations found" + strings.Repeat(" ", 54) + " |\n" +
		"+----------+---------+-------------+------+--------------+-------+----------+\n"

	if got := DumpInfoAsciiTable(nil); got != want {
		t.Fatalf("empty table mismatch\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestDumpInfoMigrationIDs(t *testing.T) {
	entries := []InfoEntry{
		{Version: "1", VersionForID: "1", Description: "one", Type: TypeSQLMigration},
		{Version: "2", VersionForID: "2", Description: "two", Type: TypeSQLMigration},
		{Version: "null", VersionNull: true, Description: "repeatable", Type: TypeSQLMigration},
		{Version: "1", VersionForID: "1", Description: "undo", Type: TypeSQLUndo},
	}
	if got, want := DumpInfoMigrationIDs(entries), "1,2,repeatable"; got != want {
		t.Fatalf("DumpInfoMigrationIDs() = %q, want %q", got, want)
	}
}
