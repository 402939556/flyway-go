//go:build !windows

package core

import "strings"

// isHiddenDirectory mirrors java.io.File.isHidden() on Unix-like systems,
// where a leading dot denotes a hidden file or directory.
func isHiddenDirectory(path, name string) bool {
	_ = path
	return strings.HasPrefix(name, ".")
}
