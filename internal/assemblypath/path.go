// Package assemblypath resolves existing assembly sources and include roots.
package assemblypath

import (
	"fmt"
	"os"
	"path/filepath"
)

// Canonical returns a resolved absolute path to the same existing file.
// Callers still enforce their source-root and directory-kind restrictions.
func Canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	original, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	resolved, err := resolveExistingPath(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve existing assembly path %q: %w", absolute, err)
	}
	final, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !os.SameFile(original, final) {
		return "", fmt.Errorf("assembly path %q changed filesystem identity during resolution", absolute)
	}
	return resolved, nil
}
