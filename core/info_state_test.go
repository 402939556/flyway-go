package core

import (
	"strings"
	"testing"
	"time"
)

func TestParseValidatePatternOfficialMessages(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{
			name:    "invalid type",
			pattern: "bogus:*",
			want:    "Invalid migration type 'bogus'. Valid types are: [*, repeatable, versioned]",
		},
		{
			name:    "invalid state",
			pattern: "*:bogus",
			want:    "Invalid migration state 'bogus'. Valid states are: [*, missing, pending, ignored, future]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseValidatePattern(tt.pattern)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
			if strings.Contains(err.Error(), "，") {
				t.Fatalf("error contains a full-width comma: %q", err.Error())
			}
		})
	}
}
func filterTestInfo(version string, installedOn time.Time, state MigrationState) *MigrationInfo {
	ctx := newMigrationInfoContext()
	v := MustParseVersion(version)
	ctx.LastResolved = v
	ctx.LastApplied = v
	m := &Migration{
		Kind:        KindVersioned,
		Version:     v,
		Description: "test",
		Script:      "V" + version + "__test.sql",
	}
	info := &MigrationInfo{Resolved: m, Context: ctx}
	if state == StateAvailable {
		stateOverride := StateAvailable
		info.stateOverride = &stateOverride
	}
	if !installedOn.IsZero() {
		info.Applied = &SchemaHistoryRow{
			Version:      version,
			VersionValid: true,
			Description:  "test",
			Type:         TypeSQLMigration,
			Script:       "V" + version + "__test.sql",
			InstalledOn:  installedOn,
			Success:      true,
		}
	}
	switch state {
	case StateDeleted:
		info.Deleted = true
	case StateUndone:
		info.Undone = true
	}
	return info
}

func TestInfoFilterDateAndVersionBoundaries(t *testing.T) {
	installed := time.Date(2020, 10, 13, 16, 30, 0, 0, time.UTC)
	info := filterTestInfo("2", installed, StateSuccess)
	since := installed
	until := installed
	v2 := MustParseVersion("2")
	v19 := MustParseVersion("1.9")
	v21 := MustParseVersion("2.1")

	tests := []struct {
		name   string
		filter *InfoFilter
		want   bool
	}{
		{name: "since date is strict after", filter: &InfoFilter{SinceDate: &since}, want: false},
		{name: "until date is strict before", filter: &InfoFilter{UntilDate: &until}, want: false},
		{name: "since version includes equal", filter: &InfoFilter{SinceVersion: &v2}, want: true},
		{name: "since version excludes lower", filter: &InfoFilter{SinceVersion: &v21}, want: false},
		{name: "until version includes equal", filter: &InfoFilter{UntilVersion: &v2}, want: true},
		{name: "until version excludes higher", filter: &InfoFilter{UntilVersion: &v19}, want: false},
		{name: "matching one state", filter: &InfoFilter{States: []MigrationState{StatePending, StateSuccess}}, want: true},
		{name: "non-matching state", filter: &InfoFilter{States: []MigrationState{StatePending}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Matches(info); got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInfoFilterRepeatableVersionSemantics(t *testing.T) {
	ctx := newMigrationInfoContext()
	m := &Migration{Kind: KindRepeatable, Description: "repeatable"}
	info := &MigrationInfo{Resolved: m, Context: ctx}
	since := MustParseVersion("1")
	until := MustParseVersion("2")
	if !(&InfoFilter{SinceVersion: &since}).Matches(info) {
		t.Fatal("repeatable migration should pass sinceVersion when version is null")
	}
	if (&InfoFilter{UntilVersion: &until}).Matches(info) {
		t.Fatal("repeatable migration should fail untilVersion when version is null")
	}
}

func TestInfoFilterAvailableIsAlwaysIncluded(t *testing.T) {
	info := filterTestInfo("2", time.Time{}, StateAvailable)
	filter := &InfoFilter{States: []MigrationState{StateSuccess}}
	if filter.Matches(info) {
		t.Fatal("available migration should not match the state filter")
	}
	if !shouldIncludeInfo(info, filter) {
		t.Fatal("available migration must be retained by InfoWithFilter")
	}
}

func TestBaselineAppliedStateMatchesOfficialPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		typ          string
		success      bool
		lastResolved string
		want         MigrationState
		resolved     bool
	}{
		{name: "synthetic baseline ignores failure", typ: TypeBaseline, success: false, lastResolved: "2", want: StateBaseline},
		{name: "sql baseline resolved", typ: TypeSQLBaseline, success: true, lastResolved: "1", want: StateBaseline, resolved: true},
		{name: "failed sql baseline", typ: TypeSQLBaseline, success: false, lastResolved: "2", want: StateFailed},
		{name: "failed missing sql baseline still failed", typ: TypeSQLBaseline, success: false, lastResolved: "2", want: StateFailed},
		{name: "missing sql baseline", typ: TypeSQLBaseline, success: true, lastResolved: "2", want: StateMissingSuccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newMigrationInfoContext()
			ctx.LastResolved = MustParseVersion(tt.lastResolved)
			ctx.LastApplied = MustParseVersion("1")
			row := SchemaHistoryRow{
				InstalledRank: 1,
				Version:       "1",
				VersionValid:  true,
				Description:   "baseline",
				Type:          tt.typ,
				Script:        "B1__baseline.sql",
				InstalledOn:   time.Now(),
				Success:       tt.success,
			}
			info := &MigrationInfo{Applied: &row, Context: ctx}
			if tt.resolved {
				info.Resolved = &Migration{Kind: KindBaseline, Version: MustParseVersion("1"), Description: "baseline", Script: "B1__baseline.sql"}
			}
			if got := info.State(); got != tt.want {
				t.Fatalf("State() = %s, want %s", got, tt.want)
			}
		})
	}
}
