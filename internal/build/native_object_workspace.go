package build

import (
	"os"
	"path/filepath"
	"sync"
)

// nativeObjectWorkspace owns auxiliary C/C++/assembly outputs until archive
// publication. Go's ExportFile identifies shared source/export data, not a
// native compilation recipe: other builds can use the same path with different
// targets, flags or source overlays. Never put writable native outputs there.
type nativeObjectWorkspace struct {
	mu  sync.Mutex
	dir string
}

func (c *context) nativeObjectWorkspace() *nativeObjectWorkspace {
	if c.nativeObjectsOwner != nil {
		return c.nativeObjectsOwner
	}
	return &c.nativeObjects
}

func (w *nativeObjectWorkspace) create(source string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dir == "" {
		dir, err := os.MkdirTemp("", "llgo-native-*")
		if err != nil {
			return "", err
		}
		w.dir = dir
	}
	// The suffix also isolates same-basename inputs within one invocation.
	file, err := os.CreateTemp(w.dir, filepath.Base(source)+"-*.o")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func (w *nativeObjectWorkspace) cleanup() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
		w.dir = ""
	}
}
