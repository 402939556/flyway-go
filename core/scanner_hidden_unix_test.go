//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScannerSkipsHiddenDirectory(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "V1__hidden.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "V1__visible.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{Naming: DefaultNaming()}
	migrations, err := s.Scan("filesystem:" + dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Script != "V1__visible.sql" {
		t.Fatalf("migrations = %#v, want only V1__visible.sql", migrations)
	}
}
