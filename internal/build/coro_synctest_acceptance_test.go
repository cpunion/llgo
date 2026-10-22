//go:build !llgo

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

// These are real LLGo compile/link/run tests. Host execution-domain unit and
// race tests cannot substitute for standard-library bridge/GC acceptance.
func TestCoroSynctestAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_SYNCTEST_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_SYNCTEST_ACCEPTANCE=1 to run the linked synctest gate")
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
	for _, fixture := range []struct{ name, output string }{
		{"coro_synctest_go127", "synctest Run/Wait/channel/select/Cond/Sleep/Timer/Ticker/AfterFunc ok"},
		{"coro_synctest_context_go127", "synctest context-free child receive ok"},
		{"coro_synctest_objects_go127", "synctest heap association/WaitGroup/weak ownership ok"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "synctest")
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
			defer cancel()
			build := exec.CommandContext(ctx, compiler, "build", "-o", image, "./_testgo/"+fixture.name)
			build.Dir = filepath.Join(root, "runtime")
			build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("LLGo synctest build: %v\n%s", err, output)
			}
			runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer runCancel()
			run := exec.CommandContext(runCtx, image)
			output, err := run.CombinedOutput()
			if err != nil || !strings.Contains(string(output), fixture.output) {
				t.Fatalf("LLGo synctest run: %v, deadline: %v\n%s", err, runCtx.Err(), output)
			}
		})
	}
}
