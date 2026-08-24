//go:build !llgo

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroWaitGroupEffectDiagnostic(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main

import "sync"

func main() {
	var group sync.WaitGroup
	group.Add(1)
	go func() { group.Done() }()
	group.Wait()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	seen := make(map[string]bool)
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		for _, function := range plan.Functions() {
			fn := function.Function
			if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
				continue
			}
			path := fn.Pkg.Pkg.Path()
			name := fn.Name()
			if !strings.Contains(path, "goplus/llgo/runtime/internal/") && path != "sync" {
				continue
			}
			switch name {
			case "Add", "Done", "semaRelease", "sync_runtime_Semrelease",
				"llgoCoroSemaphoreReleaseOrAbortV2",
				"__llgo_coro_sema_release_or_abort_v2",
				"coroKeyedPostClaimedExternalV2",
				"coroTargetPostKeyedOperationV2",
				"coroNativeFleetFinishExecutorRequestV1",
				"coroNativeMActivateDeferredReplacementV1",
				"coroNativeMRequestPhysicalOwnerV1",
				"coroNativeMStartPhysicalOwnerV1",
				"nativePipeWrite",
				"nativeCDoorbellWrite",
				"RequestReuseOwner",
				"TryReuseOwner",
				"CreateOwner":
				key := path + "\x00" + fn.String()
				if !seen[key] {
					seen[key] = true
					t.Logf("%s %s: %+v", path, fn.String(), function.Plan)
				}
			}
		}
	}
	if _, err := Do([]string{"file=" + source}, conf); err != nil {
		t.Fatalf("build WaitGroup fixture: %v", err)
	}
}
