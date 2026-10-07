package build

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
	conf := NewDefaultConf(ModeBuild)
	conf.OutFile = output
	if _, err := Do([]string{"."}, conf); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(output).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("native thread C exports: %v\n%s", err, out)
	}
}
