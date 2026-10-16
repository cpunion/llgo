//go:build darwin || linux

package build

import (
	stdcontext "context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBDWGCNotificationAutomaticForeignThreadAndDetach(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "bdw-gc").Output()
	if err != nil {
		t.Skip("BDWGC development files are unavailable")
	}
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "notification")
	args := []string{"-O2", "-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread",
		"testdata/gcnotify/main.c", "../../runtime/internal/clite/bdwgc/_wrap/coro_gc_notify.c",
		"../../runtime/internal/corodoorbell/_wrap/doorbell.c", "-o", binary}
	args = append(args, strings.Fields(string(flags))...)
	if output, err := exec.CommandContext(ctx, clang, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile GC notification test: %v\n%s", err, output)
	}
	run, stop := stdcontext.WithTimeout(stdcontext.Background(), 15*time.Second)
	defer stop()
	if output, err := exec.CommandContext(run, binary).CombinedOutput(); err != nil {
		t.Fatalf("automatic GC notification/descriptor lifecycle: %v\n%s", err, output)
	}
}
