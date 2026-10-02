//go:build !windows

package assemblypath

import "path/filepath"

func resolveExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
