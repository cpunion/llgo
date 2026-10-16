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

func TestCoroReflectHeaderGCAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_TIMER_GC_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_TIMER_GC_ACCEPTANCE=1 for real header provenance/GC acceptance")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, source, output string }{
		{"goroot", filepath.Join(runtime.GOROOT(), "test", "unsafe_string_data.go"), ""},
		{"reflect-pointer-goroot", filepath.Join(runtime.GOROOT(), "test", "fixedbugs", "issue15329.go"), ""},
		{"reflect-unsafeaddr-goroot", filepath.Join(runtime.GOROOT(), "test", "fixedbugs", "issue35073a.go"), ""},
		{"global-slice-header-goroot", filepath.Join(runtime.GOROOT(), "test", "fixedbugs", "issue35027.go"), ""},
		{"gc-await", "./internal/build/_testgo/coro_reflect_header_gc", "reflect header exact owner survives GC and await"},
		{"consumed-pointer-result", "./internal/build/_testgo/coro_consumed_pointer_gc", "consumed pointer result is collectible with a live caller"},
		{"dynamic-pointer-result", "./internal/build/_testgo/coro_dynamic_pointer_result", "dynamic pointer results survive single consumption and GC"},
		{"shared-caller-pc", filepath.Join(root, "runtime", "_testgo", "coro_shared_caller_pc", "main.go"), "shared caller PC identity passed"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "header")
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 5*time.Minute)
			defer cancel()
			build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, fixture.source)
			build.Dir = root
			build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("header provenance build: %v\n%s", err, output)
			}
			runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer runCancel()
			if output, err := exec.CommandContext(runCtx, image).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != fixture.output {
				t.Fatalf("header provenance/GC run: %v\n%s", err, output)
			}
		})
	}
}
