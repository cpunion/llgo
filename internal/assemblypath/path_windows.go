package assemblypath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const windowsPathUnits = 32768

// Resolve through a real file handle, including directory mount points. Go's
// lexical symlink walk treats these reparse ancestors as irregular files and
// cannot traverse them. Do not fall back to an unresolved spelling: callers
// use this physical path to enforce source-root boundaries.
func resolveExistingPath(path string) (resolved string, err error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := windows.CloseHandle(handle); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close assembly path handle: %w", closeErr))
		}
	}()

	return finalWindowsPath(func(buffer []uint16) (uint32, error) {
		// Flags zero means FILE_NAME_NORMALIZED | VOLUME_NAME_DOS, not the
		// opened lexical name. Keep the extended prefix for long paths and
		// identical volume semantics in subsequent containment checks.
		return windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	})
}

func finalWindowsPath(query func([]uint16) (uint32, error)) (string, error) {
	buffer := make([]uint16, 260)
	for attempt := 0; attempt < 4; attempt++ {
		n, err := query(buffer)
		if err != nil {
			return "", err
		}
		if n == 0 || n >= windowsPathUnits {
			return "", fmt.Errorf("invalid final assembly path length: %d UTF-16 units", n)
		}
		if int(n) >= len(buffer) {
			buffer = make([]uint16, int(n)+1)
			continue
		}
		for _, unit := range buffer[:n] {
			if unit == 0 {
				return "", fmt.Errorf("final assembly path contains an embedded NUL")
			}
		}
		resolved := windows.UTF16ToString(buffer[:n])
		if !strings.HasPrefix(resolved, `\\?\`) || !filepath.IsAbs(resolved) {
			return "", fmt.Errorf("final assembly path is not an absolute DOS path: %q", resolved)
		}
		return resolved, nil
	}
	return "", fmt.Errorf("final assembly path did not stabilize within the resolution bound")
}
