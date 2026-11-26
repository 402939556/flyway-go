package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func callbackTestFlyway(t *testing.T, cfg Config) *Flyway {
	t.Helper()
	cfg.setDefaults()
	dialect, err := GetDialect(cfg.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return &Flyway{cfg: cfg, dialect: dialect}
}

func encodeUTF16LEWithBOM(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := []byte{0xff, 0xfe}
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func TestLoadCallbacksFilesystemPrefixEncodingAndMetadata(t *testing.T) {
	dir := t.TempDir()
	sqlPath := filepath.Join(dir, "afterMigrate__unicode.sql")
	if err := os.WriteFile(sqlPath, encodeUTF16LEWithBOM("-- 中文\nSELECT '中文';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sqlPath+".conf", []byte("encoding=UTF-16LE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := callbackTestFlyway(t, Config{
		Driver:            "postgres",
		CallbackLocations: []string{"filesystem:" + dir},
	})
	callbacks, err := f.loadCallbacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("callbacks = %#v, want one", callbacks)
	}
	cb := callbacks[0]
	if cb.Event != EventAfterMigrate {
		t.Fatalf("event = %v, want %v", cb.Event, EventAfterMigrate)
	}
	if cb.Description != "unicode" {
		t.Fatalf("description = %q", cb.Description)
	}
	if len(cb.SQL) != 1 || !strings.Contains(cb.SQL[0], "中文") {
		t.Fatalf("decoded SQL = %#v, want Chinese text", cb.SQL)
	}
}

func TestLoadCallbacksSkipDefaultCallbacks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "beforeMigrate.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := callbackTestFlyway(t, Config{
		Driver:               "postgres",
		CallbackLocations:    []string{"filesystem:" + dir},
		SkipDefaultCallbacks: Bool(true),
	})
	callbacks, err := f.loadCallbacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 0 {
		t.Fatalf("callbacks = %#v, want none", callbacks)
	}
}

func TestLoadCallbacksExplicitMissingLocationIsSkipped(t *testing.T) {
	f := callbackTestFlyway(t, Config{
		Driver:                 "postgres",
		CallbackLocations:      []string{"filesystem:" + filepath.Join(t.TempDir(), "missing")},
		FailOnMissingLocations: Bool(true),
	})
	callbacks, err := f.loadCallbacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 0 {
		t.Fatalf("callbacks = %#v, want none", callbacks)
	}
}

func TestLoadCallbacksDetectsDuplicateByResourceName(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, "beforeMigrate__dup.sql"), []byte("SELECT 1;"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := callbackTestFlyway(t, Config{
		Driver: "postgres",
		CallbackLocations: []string{
			"filesystem:" + first,
			"filesystem:" + second,
		},
	})
	_, err := f.loadCallbacks()
	if err == nil || !strings.Contains(err.Error(), "Found more than 1 SQL callback script called beforeMigrate__dup!") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCallbacksDeduplicatesNestedLocation(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "beforeMigrate__once.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := callbackTestFlyway(t, Config{
		Driver: "postgres",
		CallbackLocations: []string{
			"filesystem:" + sub,
			"filesystem:" + dir,
		},
	})
	callbacks, err := f.loadCallbacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("callbacks = %#v, want one", callbacks)
	}
}
