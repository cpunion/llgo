//go:build !llgo && !wasm

package cgo

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/build"
)

// Build an ordinary executable: a testing entry can hide missing guards on
// main-package exports. The LLGo CI matrix also runs this fixture directly.
func TestCExportForeignThreadsFromExecutableAndDependency(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("hosted native C callbacks")
	}
	dir, err := filepath.Abs("testdata/foreigncallback")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "callback")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	t.Chdir(dir)
	conf := build.NewDefaultConf(build.ModeBuild)
	conf.OutFile = output
	if _, err := build.Do([]string{"."}, conf); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(output).CombinedOutput()
	const want = "Go-thread reentry: ok\nC-thread reentry: ok\nok"
	if err != nil || strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")) != want {
		t.Fatalf("native thread C exports: %v\n%s", err, out)
	}
}
