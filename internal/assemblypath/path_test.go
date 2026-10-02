package assemblypath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalExistingAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "original.h")
	if err := os.WriteFile(file, []byte("#define VALUE 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		resolved, err := Canonical(path)
		if err != nil || !filepath.IsAbs(resolved) {
			t.Fatalf("original path %q: %q, %v", path, resolved, err)
		}
		original, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		final, err := os.Stat(resolved)
		if err != nil || !os.SameFile(original, final) {
			t.Fatalf("resolved identity differs: %q, %v", resolved, err)
		}
	}
	if _, err := Canonical(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path did not preserve its actual error: %v", err)
	}
}

func TestCanonicalOriginalGoToolchainPaths(t *testing.T) {
	for _, relative := range []string{
		".", "src/internal/cpu", "src/internal/bytealg", "pkg/include",
		"pkg/include/textflag.h", "pkg/include/funcdata.h",
	} {
		t.Run(relative, func(t *testing.T) {
			original := filepath.Join(runtime.GOROOT(), filepath.FromSlash(relative))
			resolved, err := Canonical(original)
			if err != nil {
				t.Fatal(err)
			}
			first, err := os.Stat(original)
			if err != nil {
				t.Fatal(err)
			}
			last, err := os.Stat(resolved)
			if err != nil || !os.SameFile(first, last) {
				t.Fatalf("actual selected toolchain identity changed: %v", err)
			}
			if !first.IsDir() {
				body, err := os.ReadFile(resolved)
				if err != nil || len(body) == 0 {
					t.Fatalf("resolved original header: bytes=%d error=%v", len(body), err)
				}
			}
		})
	}
}
