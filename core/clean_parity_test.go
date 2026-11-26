package core

import (
	"reflect"
	"testing"
	"time"
)

func TestFormatInstantParity(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zeroFrac", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), "2024-01-02T03:04:05Z"},
		{"millis", time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), "2024-01-02T03:04:05.123Z"},
		{"micros", time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC), "2024-01-02T03:04:05.123456Z"},
		{"nanos", time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC), "2024-01-02T03:04:05.123456789Z"},
		{"convertedToUTC", time.Date(2024, 1, 2, 11, 4, 5, 0, time.FixedZone("CST", 8*60*60)), "2024-01-02T03:04:05Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatInstant(tc.in); got != tc.want {
				t.Fatalf("formatInstant() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatLocalTimestampParity(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zeroFrac", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), "2024-01-02 03:04:05.0"},
		{"millis", time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), "2024-01-02 03:04:05.123"},
		{"micros", time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC), "2024-01-02 03:04:05.123456"},
		{"nanos", time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC), "2024-01-02 03:04:05.123456789"},
		{"trailingZero", time.Date(2024, 1, 2, 3, 4, 5, 900980000, time.UTC), "2024-01-02 03:04:05.90098"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatLocalTimestamp(tc.in); got != tc.want {
				t.Fatalf("formatLocalTimestamp() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatInstalledOnUTCUsesLocalWallClock(t *testing.T) {
	oldLocal := time.Local
	t.Cleanup(func() { time.Local = oldLocal })
	wallClock := time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC)
	for _, tc := range []struct {
		name string
		zone *time.Location
		want string
	}{
		{"utc", time.UTC, "2024-01-02T03:04:05.123456Z"},
		{"shanghai", time.FixedZone("Asia/Shanghai", 8*60*60), "2024-01-01T19:04:05.123456Z"},
		{"newYorkFixed", time.FixedZone("UTC-5", -5*60*60), "2024-01-02T08:04:05.123456Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			time.Local = tc.zone
			if got := formatInstalledOnUTC(wallClock); got != tc.want {
				t.Fatalf("formatInstalledOnUTC() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJavaSplitCommaParity(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{",", []string{}},
		{"a,", []string{"a"}},
		{"a,,b", []string{"a", "", "b"}},
		{",a", []string{"", "a"}},
		{"a,,,", []string{"a"}},
		{",,", []string{}},
		{"a,,b,,", []string{"a", "", "b"}},
	}
	for _, tc := range cases {
		if got := javaSplitComma(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("javaSplitComma(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestDialectUnQuoteParity(t *testing.T) {
	pg := postgresDialect{}
	my := mysqlDialect{}
	pgCases := []struct {
		in, want string
	}{
		{`"plain"`, "plain"},
		{`"a""b"`, `a"b`},
		{"plain", "plain"},
		{`"unterminated`, `"unterminated`},
	}
	for _, tc := range pgCases {
		if got := pg.UnQuote(tc.in); got != tc.want {
			t.Errorf("postgres UnQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	myCases := []struct {
		in, want string
	}{
		{"`plain`", "plain"},
		{"`a``b`", "a``b"},
		{"plain", "plain"},
		{"`unterminated", "`unterminated"},
	}
	for _, tc := range myCases {
		if got := my.UnQuote(tc.in); got != tc.want {
			t.Errorf("mysql UnQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPostgresSearchPathForSchema(t *testing.T) {
	cases := []struct {
		name, current, schema, quoted, want string
		changed                             bool
	}{
		{"same", "public", "public", `"public"`, "public", false},
		{"alreadyLeading", "public,foo", "public", `"public"`, "public,foo", false},
		{"prepend", "foo", "public", `"public"`, `"public",foo`, true},
		{"empty", "", "public", `"public"`, `"public"`, true},
		{"quotedSchema", "public", "my schema", `"my schema"`, `"my schema",public`, true},
		{"prefixIsNotSchema", "public2,foo", "public", `"public"`, `"public",public2,foo`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := postgresSearchPathForSchema(tc.current, tc.schema, tc.quoted)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("postgresSearchPathForSchema() = (%q, %v), want (%q, %v)", got, changed, tc.want, tc.changed)
			}
		})
	}
}
