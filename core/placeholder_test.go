package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlaceholderChecksumScopes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"V1__versioned.sql": "CREATE TABLE ${table_name} (id INT);\n",
		"R__repeatable.sql": "CREATE OR REPLACE VIEW ${view_name} AS SELECT 1;\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Scanner{
		Naming:            DefaultNaming(),
		Placeholders:      map[string]string{"TABLE_NAME": "placeholder_table", "VIEW_NAME": "placeholder_view"},
		PlaceholderPrefix: "${",
		PlaceholderSuffix: "}",
	}
	migrations, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var versioned, repeatable *Migration
	for _, m := range migrations {
		if m.Kind == KindVersioned {
			versioned = m
		}
		if m.Kind == KindRepeatable {
			repeatable = m
		}
	}
	if versioned == nil || repeatable == nil {
		t.Fatalf("expected both migration kinds, got %d", len(migrations))
	}
	rawVersioned := "CREATE TABLE ${table_name} (id INT);\n"
	rawRepeatable := "CREATE OR REPLACE VIEW ${view_name} AS SELECT 1;\n"
	if versioned.Checksum != Checksum(rawVersioned) {
		t.Fatalf("versioned checksum should use raw content: got %d, want %d", versioned.Checksum, Checksum(rawVersioned))
	}
	if repeatable.Checksum != Checksum("CREATE OR REPLACE VIEW placeholder_view AS SELECT 1;\n") {
		t.Fatalf("repeatable checksum should use replaced content: got %d", repeatable.Checksum)
	}
	if repeatable.EquivalentChecksum == nil || *repeatable.EquivalentChecksum != Checksum(rawRepeatable) {
		t.Fatalf("repeatable equivalent checksum should use raw content")
	}
}

func TestReplacePlaceholdersMatchesReader(t *testing.T) {
	values := map[string]string{"NAME": "value"}
	got, err := ReplacePlaceholders("a${name}b", values, "${", "}", ":")
	if err != nil || got != "avalueb" {
		t.Fatalf("got %q, %v", got, err)
	}
	// A value of null/empty removes the placeholder marker in Java's reader.
	got, err = ReplacePlaceholders("a${empty}b", map[string]string{"empty": ""}, "${", "}", ":")
	if err != nil || got != "ab" {
		t.Fatalf("empty replacement got %q, %v", got, err)
	}
	_, err = ReplacePlaceholders("a${missing}b", values, "${", "}", ":")
	want := "No value provided for placeholder: ${missing}.  Check your configuration!"
	if err == nil || err.Error() != want {
		t.Fatalf("missing placeholder error got %v, want %q", err, want)
	}
}

func TestReplacePlaceholderDefault(t *testing.T) {
	got, err := ReplacePlaceholders("CREATE TABLE ${item:ph_item} (id INT);", nil, "${", "}", ":")
	if err != nil || got != "CREATE TABLE ph_item (id INT);" {
		t.Fatalf("default got %q, %v", got, err)
	}
	got, err = ReplacePlaceholders("CREATE TABLE ${item:ph_item} (id INT);", map[string]string{"ITEM": "custom"}, "${", "}", ":")
	if err != nil || got != "CREATE TABLE custom (id INT);" {
		t.Fatalf("configured overrides default got %q, %v", got, err)
	}
	got, err = ReplacePlaceholders("${item:ph_item}", map[string]string{"item": ""}, "${", "}", ":")
	if err != nil || got != "" {
		t.Fatalf("empty configured value got %q, %v", got, err)
	}
	got, err = ReplacePlaceholders("${item:a:b}", nil, "${", "}", ":")
	if err != nil || got != "a:b" {
		t.Fatalf("default keeps extra separator got %q, %v", got, err)
	}
	got, err = ReplacePlaceholders("${item=ph_item}", nil, "${", "}", "=")
	if err != nil || got != "ph_item" {
		t.Fatalf("custom separator got %q, %v", got, err)
	}
	_, err = ReplacePlaceholders("${flyway:defaultSchema}", nil, "${", "}", ":")
	want := "Failed to populate value for default placeholder: ${flyway:defaultSchema}"
	if err == nil || err.Error() != want {
		t.Fatalf("built-in placeholder got %v, want %q", err, want)
	}
}
