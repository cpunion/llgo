package assemblypath

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalWindowsJunctionDescendants(t *testing.T) {
	// Current Go intentionally classifies mount points as ModeIrregular, not
	// symlinks. Its generic EvalSymlinks walk cannot traverse that ancestor.
	t.Setenv("GODEBUG", "winsymlink=1")
	root := t.TempDir()
	target := filepath.Join(root, "physical")
	if err := os.MkdirAll(filepath.Join(target, "include"), 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(target, "include", "header.h")
	if err := os.WriteFile(original, []byte("#define ORIGINAL 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "junction")
	command := exec.Command("cmd", "/c", "mklink", "/J", junction, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create owned directory junction: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		// Delete only the owned junction itself, never recurse through it.
		if err := os.Remove(junction); err != nil {
			t.Errorf("remove owned junction: %v", err)
		}
	})
	for _, path := range []string{junction, filepath.Join(junction, "include"), filepath.Join(junction, "include", "header.h")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("original junction path does not exist: %v", err)
		}
		resolved, err := Canonical(path)
		if err != nil {
			t.Errorf("resolve existing junction descendant %q: %v", path, err)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		physical, err := os.Stat(resolved)
		if err != nil || !os.SameFile(info, physical) {
			t.Errorf("junction resolution changed original identity: %q, %v", resolved, err)
		}
	}
}
