//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFuncInfoRecordNativeDecodeAcceptance(t *testing.T) {
	if os.Getenv("LLGO_FUNCINFO_RECORD_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_FUNCINFO_RECORD_ACCEPTANCE=1 for native record decoding")
	}
	requireNativePCLNSidecars(t)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	dir := t.TempDir()
	run := func(t *testing.T, runtimeRun bool, args ...string) string {
		t.Helper()
		deadline := 3 * time.Minute
		if runtimeRun {
			deadline = 30 * time.Second
		}
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), deadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = root
		if !runtimeRun {
			cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "GOMEMLIMIT=2GiB")
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, output)
		}
		return strings.TrimSpace(string(output))
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		compiler = filepath.Join(dir, "llgo")
		run(t, false, filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-p=1", "-o", compiler, "./cmd/llgo")
	}
	t.Setenv("LLGO_AR", "")
	t.Setenv("LDFLAGS", "")
	t.Setenv("GOGC", "100")
	for _, mode := range []string{"embedded", "external", "none"} {
		t.Run(mode, func(t *testing.T) {
			image := filepath.Join(dir, "record-"+mode)
			run(t, false, compiler, "build", "-p=1", "-Os", "-trimpath", "-pclntab="+mode,
				"-o", image, "./internal/build/testdata/funcinfo_record_decode")
			wantSize := "28"
			if mode == "embedded" && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				wantSize = "16"
			}
			for _, procs := range []string{"1", "4"} {
				t.Setenv("GOMAXPROCS", procs)
				t.Setenv("GOMEMLIMIT", "off")
				if got := run(t, true, image, wantSize); got != "funcinfo record decode ok" {
					t.Fatalf("%s P%s = %q", mode, procs, got)
				}
			}
		})
	}
}
