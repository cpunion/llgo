//go:build !llgo

package targets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderRejectsInvalidNamesAndCycles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("outside", `{"goos":"linux"}`)
	write("escape", `{"inherits":["../outside"]}`)
	write("cycle-a", `{"inherits":["cycle-b"]}`)
	write("cycle-b", `{"inherits":["cycle-a"]}`)

	loader := NewLoader(dir)
	for _, name := range []string{"", "../outside", "a/b", `a\\b`, "with.dot"} {
		if _, err := loader.Load(name); err == nil || !strings.Contains(err.Error(), "target name") {
			t.Errorf("Load(%q) error = %v", name, err)
		}
	}
	if _, err := loader.Load("escape"); err == nil || !strings.Contains(err.Error(), "invalid target name") {
		t.Fatalf("invalid inherited name error = %v", err)
	}
	if _, err := loader.Load("cycle-a"); err == nil || !strings.Contains(err.Error(), "inheritance cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}
