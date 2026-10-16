package gotest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCallerAcceptanceMainPanicWithPeers(t *testing.T) {
	root := findRepoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "test/go/testdata/main_panic_peers/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	_, dir := prepareCallerAcceptanceProbe(t, string(source))
	image := testExecutablePath(dir, "main-panic")
	buildContext, buildCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer buildCancel()
	build := exec.CommandContext(buildContext, acceptanceLLGoBinary(t), "build", "-p=1", "-o", image, "main.go")
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build main panic probe: %v\n%s", err, output)
	}
	// Build once, then cover both the command executor and peer-P scheduling.
	for _, mode := range []string{"last", "parked", "ready"} {
		for _, procs := range []int{1, 2} {
			t.Run(mode+"/P"+strconv.Itoa(procs), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				run := exec.CommandContext(ctx, image, mode)
				run.Env = append(os.Environ(), "GOMAXPROCS="+strconv.Itoa(procs))
				output, err := run.CombinedOutput()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 2 || ctx.Err() != nil {
					t.Fatalf("main panic must terminate with status 2: %v\n%s", err, output)
				}
				for _, want := range []string{
					"panic: main-panic-with-peers", "goroutine 1 [running]:",
					"main.mainPanicSite(...)", "main.main(...)",
					"main.go:" + markerLineOf(t, string(source), "MAIN_PANIC_MARK"),
				} {
					if !strings.Contains(string(output), want) {
						t.Fatalf("main panic trace missing %q:\n%s", want, output)
					}
				}
				if strings.Contains(string(output), "fatal error:") {
					t.Fatalf("valid main panic became a runtime invariant failure:\n%s", output)
				}
			})
		}
	}
}
