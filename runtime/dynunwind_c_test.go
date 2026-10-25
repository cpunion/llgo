//go:build !llgo && (darwin || linux) && (amd64 || arm64)

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func dynUnwindContractImage(t *testing.T, fixture string, realFault bool) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler unavailable")
	}
	dir := t.TempDir()
	copySource := func(source, destination string) {
		t.Helper()
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, destination), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	copySource("internal/lib/runtime/_wrap/dynunwind.c", "dynunwind-under-test.c")
	copySource(filepath.Join("testdata", "dynunwind", fixture), "main.c")
	image := filepath.Join(dir, "contract")
	args := []string{"-std=c11", "-Os", "-Wall", "-Wextra", "-Werror",
		"-fomit-frame-pointer", "-fno-optimize-sibling-calls",
		"-funwind-tables", "-fasynchronous-unwind-tables", filepath.Join(dir, "main.c")}
	if realFault {
		copySource("internal/lib/runtime/_wrap/fault.c", "fault.c")
		args = append(args, filepath.Join(dir, "fault.c"))
		if goruntime.GOOS == "darwin" {
			args = append(args, "-Wl,-export_dynamic")
		} else {
			args = append(args, "-Wl,--export-dynamic")
		}
	}
	if goruntime.GOOS == "linux" {
		args = append(args, "-ldl")
	}
	args = append(args, "-o", image)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile unwind contract: %v\n%s", err, out)
	}
	return image
}

func runDynUnwindContract(t *testing.T, image string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, image, args...)
	cmd.Env = append(os.Environ(), "LLGO_DYNUNWIND_DEBUG=0", "LLGO_DYNUNWIND_NAMES=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unwind contract %v: %v\n%s", args, err, out)
	}
	t.Logf("%s", out)
	return string(out)
}

func TestDynUnwindScratchInitializationContract(t *testing.T) {
	image := dynUnwindContractImage(t, "init_contract.c", false)
	for _, mode := range []string{"disabled", "unresolved", "mapping-failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			runDynUnwindContract(t, image, mode)
		})
	}
}

func TestDynUnwindRealFaultContexts(t *testing.T) {
	if os.Getenv("LLGO_DYNUNWIND_FAULT_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_DYNUNWIND_FAULT_ACCEPTANCE=1 with a local libunwind provider for the real fault gate")
	}
	image := dynUnwindContractImage(t, "fault_contract.c", true)
	for _, mode := range []string{"legacy", "coro"} {
		for _, setting := range []string{"default", "disabled"} {
			t.Run(mode+"/"+setting, func(t *testing.T) {
				out := runDynUnwindContract(t, image, mode, setting)
				if !strings.Contains(out, "dynunwind real fault contract ok") || !strings.Contains(out, "faults=16") {
					t.Fatalf("missing fault recovery receipt: %s", out)
				}
			})
		}
	}
}
