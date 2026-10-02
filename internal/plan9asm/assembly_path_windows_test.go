package plan9asm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssemblyIncludeRejectsPhysicalWindowsJunctionEscape(t *testing.T) {
	t.Setenv("GODEBUG", "winsymlink=1")
	root := t.TempDir()
	dir := filepath.Join(root, "package")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{dir, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "header.h"), []byte("#define ESCAPED 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(dir, "escape")
	command := exec.Command("cmd", "/c", "mklink", "/J", junction, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create owned junction escape fixture: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Errorf("remove owned junction only: %v", err)
		}
	})
	pkg := mustTestPackage(t, "example.org/junction", "package junction\n")
	pkg.Dir = dir
	_, err := preprocessAssemblyForPkg(pkg, filepath.Join(dir, "original.s"),
		[]byte("#include \"escape/header.h\"\n"), nil, "windows", "amd64", TranslateOptions{})
	if err == nil || !strings.Contains(err.Error(), "escapes through a symlink") {
		t.Fatalf("physical junction escape was not rejected by the source-root guard: %v", err)
	}
}
