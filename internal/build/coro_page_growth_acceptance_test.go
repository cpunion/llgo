//go:build !llgo && (darwin || linux)

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoroNativeOperationPageGrowthAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_PAGE_GROWTH_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_PAGE_GROWTH_ACCEPTANCE=1 for native expanded-page GC proof")
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
	image := filepath.Join(t.TempDir(), "page-growth")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-Os", "-trimpath", "-ldflags=-s -w", "-o", image, "./internal/coro/testdata/native_page_growth")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("page growth build: %v\n%s", err, output)
	}
	for _, procs := range []string{"1", "4"} {
		for _, trial := range []string{"1", "2", "3"} {
			t.Run("P"+procs+"/trial"+trial, func(t *testing.T) {
				ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 60*time.Second)
				defer cancel()
				run := exec.CommandContext(ctx, image)
				run.Env = append(os.Environ(), "GOMAXPROCS="+procs, "GOGC=100", "GOMEMLIMIT=off")
				if output, err := run.CombinedOutput(); err != nil || !strings.Contains(string(output), "page growth gc ok page 4 block 256 capacity 1032 same-kind") {
					t.Fatalf("page growth GC run: %v\n%s", err, output)
				} else {
					t.Logf("%s", output)
				}
			})
		}
	}
}
