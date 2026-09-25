//go:build !llgo && (darwin || linux || windows)

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNativeTracebackCaptureFaultAndTimeout(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is required for the native signal transport test")
	}
	bin := filepath.Join(t.TempDir(), "traceback-native")
	args := []string{"-std=c11", "-O2", "-fno-omit-frame-pointer", "-Wall", "-Wextra", "-Werror", "-I../../runtime/internal/stacktrace/_wrap"}
	if runtime.GOOS == "windows" {
		bin += ".exe"
		if target := os.Getenv("LLGO_WINDOWS_TARGET_TRIPLE"); target != "" {
			args = append(args, "--target="+target)
		}
		args = append(args, "-fuse-ld=lld", "testdata/tracebacknative/windows.c",
			"../../runtime/internal/runtime/_wrap/setjmp_windows_amd64.c",
			"../../runtime/internal/runtime/_wrap/setjmp_windows_arm64.c")
	} else {
		args = append(args, "-pthread", "testdata/tracebacknative/main.c")
	}
	cmd := exec.Command(clang, append(args, "-o", bin)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile native transport: %v\n%s", err, out)
	}
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, bin).CombinedOutput(); err != nil {
		t.Fatalf("native transport: %v\n%s", err, out)
	}
}
