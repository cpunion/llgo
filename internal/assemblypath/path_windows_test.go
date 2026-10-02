package assemblypath

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
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

	longRelative := filepath.Join("include", "long-directory-component", "long-directory-component",
		"long-directory-component", "long-directory-component", "long-directory-component",
		"long-directory-component", "long-directory-component", "long-directory-component",
		"long-directory-component", "long-directory-component", "header.h")
	longPhysical := filepath.Join(target, longRelative)
	if err := os.MkdirAll(filepath.Dir(longPhysical), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(longPhysical, []byte("#define LONG 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Canonical(filepath.Join(junction, longRelative))
	if err != nil {
		t.Fatalf("resolve long original junction path: %v", err)
	}
	if body, err := os.ReadFile(resolved); err != nil || string(body) != "#define LONG 1\n" {
		t.Fatalf("long original header changed: %q, %v", body, err)
	}
}

func TestFinalWindowsPathBoundsAndErrors(t *testing.T) {
	queryFailure := errors.New("original final-path query failure")
	for _, tc := range []struct {
		name  string
		query func([]uint16) (uint32, error)
		want  error
	}{
		{"query_error", func([]uint16) (uint32, error) { return 0, queryFailure }, queryFailure},
		{"empty", func([]uint16) (uint32, error) { return 0, nil }, nil},
		{"oversized", func([]uint16) (uint32, error) { return windowsPathUnits, nil }, nil},
		{"unstable", func(b []uint16) (uint32, error) { return uint32(len(b)), nil }, nil},
		{"embedded_nul", func(b []uint16) (uint32, error) { b[0] = 'C'; return 2, nil }, nil},
		{"relative", func(b []uint16) (uint32, error) {
			copy(b, []uint16{'h', 'e', 'a', 'd', 'e', 'r'})
			return 6, nil
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if path, err := finalWindowsPath(tc.query); err == nil {
				t.Fatalf("unresolved or invalid path accepted: %q", path)
			} else if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("actual query error discarded: %v", err)
			}
		})
	}

	for _, path := range []string{`\\?\C:\physical\header.h`, `\\?\UNC\server\share\header.h`} {
		wide, err := windows.UTF16FromString(path)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := finalWindowsPath(func(buffer []uint16) (uint32, error) {
			copy(buffer, wide)
			return uint32(len(wide) - 1), nil
		})
		if err != nil || resolved != path {
			t.Fatalf("physical DOS/UNC spelling changed: %q => %q, %v", path, resolved, err)
		}
	}
}
