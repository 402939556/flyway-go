//go:build windows

package core

import (
	"syscall"
)

// isHiddenDirectory mirrors java.io.File.isHidden() on Windows: only the DOS
// hidden attribute is considered. A leading dot or underscore is not hidden.
func isHiddenDirectory(path, name string) bool {
	_ = name
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(ptr)
	if err != nil {
		return false
	}
	return attrs&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
