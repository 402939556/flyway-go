package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEncodingFlowsIntoScannerEncoding(t *testing.T) {
	cfg := Config{Encoding: "UTF-16"}
	cfg.setDefaults()
	if cfg.Naming.Encoding != "UTF-16" {
		t.Fatalf("naming encoding = %q, want UTF-16", cfg.Naming.Encoding)
	}
	if cfg.Encoding != "UTF-16" {
		t.Fatalf("global encoding = %q, want UTF-16", cfg.Encoding)
	}
}

func TestLoadSQLScriptMetadata(t *testing.T) {
	dir := t.TempDir()
	sqlPath := filepath.Join(dir, "V1__test.sql")
	if err := os.WriteFile(sqlPath+".conf", []byte("executeInTransaction=false\nplaceholderReplacement=false\nencoding=UTF-8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	md, err := LoadSQLScriptMetadata(sqlPath+".conf", nil, "${", "}")
	if err != nil {
		t.Fatal(err)
	}
	if md.ExecuteInTransaction == nil || *md.ExecuteInTransaction {
		t.Fatal("executeInTransaction should be false")
	}
	if md.PlaceholderReplacement == nil || *md.PlaceholderReplacement {
		t.Fatal("placeholderReplacement should be false")
	}
	if md.Encoding != "UTF-8" {
		t.Fatalf("encoding = %q", md.Encoding)
	}
}

func TestLoadSQLScriptMetadataMappedAndRawShouldExecute(t *testing.T) {
	dir := t.TempDir()
	sqlPath := filepath.Join(dir, "R__test.sql")
	if err := os.WriteFile(sqlPath+".conf", []byte("executeInTransaction=${tx}\nshouldExecute=${expr}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSQLScriptMetadata(sqlPath+".conf", map[string]string{"tx": "false", "expr": "true"}, "${", "}")
	if err == nil || !strings.Contains(err.Error(), "shouldExecute is not supported by OSS Edition") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSQLScriptMetadataUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "V1__x.sql.conf")
	if err := os.WriteFile(p, []byte("foo=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSQLScriptMetadata(p, nil, "${", "}")
	want := "Unknown configuration property: foo. Please check your script config files"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestMixedStatementCheck(t *testing.T) {
	stmts := []ParsedStatement{{SQL: "CREATE TABLE t(id int)", Line: 1, CanExecuteInTransaction: true}, {SQL: "CREATE INDEX CONCURRENTLY i ON t(id)", Line: 2, CanExecuteInTransaction: false}}
	err := validateMixedStatementKinds(stmts, false, nil, true)
	if err == nil || !strings.Contains(err.Error(), "same migration (even though mixed is false)") {
		t.Fatalf("unexpected: %v", err)
	}
}
