package plan9asm

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/internal/assemblypath"
)

func TestCanonicalAssemblyDirectoryRejectsRegularFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if path, err := canonicalAssemblyDirectory(file); err == nil {
		t.Fatalf("regular source file accepted as a directory: %q", path)
	}
}

func TestCanonicalAssemblyDirectoryPreservesFilesystemFailures(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := canonicalAssemblyDirectory(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing-directory error lost its identity: %v", err)
	}
}

// The package directories and headers must exist in the same original Go
// installation. In particular, a readable assembly overlay is not evidence
// that an independently supplied package directory is valid.
func TestAssemblyDirectoriesBindOriginalGoInstallation(t *testing.T) {
	root := runtime.GOROOT()
	if root == "" {
		t.Fatal("the original Go installation is required")
	}
	for _, relative := range []string{
		"src/internal/cpu", "src/internal/bytealg", "pkg/include",
	} {
		t.Run(relative, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(relative))
			canonical, err := canonicalAssemblyDirectory(path)
			if err != nil {
				logAssemblyPathComponents(t, path)
				t.Fatalf("original Go directory %q: %v", path, err)
			}
			originalInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			canonicalInfo, err := os.Stat(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !originalInfo.IsDir() || !os.SameFile(originalInfo, canonicalInfo) {
				t.Fatalf("canonical directory changed filesystem identity: %q => %q", path, canonical)
			}
		})
	}
	for _, header := range []string{"textflag.h", "funcdata.h"} {
		path := filepath.Join(root, "pkg", "include", header)
		original, err := os.ReadFile(path)
		if err != nil || len(original) == 0 {
			t.Fatalf("required original Go header %q: %v (bytes=%d)", path, err, len(original))
		}
		canonical, err := assemblypath.Canonical(path)
		if err != nil {
			logAssemblyPathComponents(t, path)
			t.Fatalf("resolve original Go header %q: %v", path, err)
		}
		originalInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		canonicalInfo, err := os.Stat(canonical)
		if err != nil || !os.SameFile(originalInfo, canonicalInfo) {
			t.Fatalf("original header identity changed: %q => %q: %v", path, canonical, err)
		}
	}
}

func logAssemblyPathComponents(t *testing.T, path string) {
	t.Helper()
	for {
		info, statErr := os.Stat(path)
		canonical, resolveErr := filepath.EvalSymlinks(path)
		if info == nil {
			t.Logf("path=%q stat=%v canonical=%q resolve=%v", path, statErr, canonical, resolveErr)
		} else {
			t.Logf("path=%q name=%q mode=%s canonical=%q resolve=%v", path, info.Name(), info.Mode(), canonical, resolveErr)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return
		}
		path = parent
	}
}
