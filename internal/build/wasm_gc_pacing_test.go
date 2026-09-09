package build

import (
	stdctx "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Run the collector's pure pacing implementation and its regression tests
// without requiring the LLGo C runtime used by the rest of tinygogc.
func TestWasmGCPacing(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "runtime", "internal", "runtime", "tinygogc"))
	if err != nil {
		t.Fatal(err)
	}
	architectures := []string{runtime.GOARCH}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		architectures = append(architectures, "386")
	}
	for _, arch := range architectures {
		t.Run(arch, func(t *testing.T) {
			ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
			defer cancel()
			// Use the nested module's language version, not the language
			// version of the host Go command's synthetic file-list package.
			cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout=20s", "-cover", ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOARCH="+arch, "CGO_ENABLED=0")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("collector pacing checks: %v\n%s", err, output)
			}
			t.Logf("collector pacing checks:\n%s", output)
		})
	}
}
