package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnderscoreDirectoryIsNotHidden(t *testing.T) {
	dir := t.TempDir()
	underscore := filepath.Join(dir, "_migrations")
	if err := os.Mkdir(underscore, 0o755); err != nil {
		t.Fatal(err)
	}
	if isHiddenDirectory(underscore, "_migrations") {
		t.Fatal("_migrations must not be hidden merely because it starts with underscore")
	}
}
