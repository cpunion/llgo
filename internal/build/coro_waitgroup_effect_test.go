//go:build !llgo

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the production source, managed-local-export borrow proof and
// emitter together. Both ordinary and durable waits share one prepare/park
// span; choosing a prepare entry in separate CFG branches loses that proof.
func TestCoroWaitGroupParkStateFrameRetention(t *testing.T) {
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
	var body string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil {
			return
		}
		function := pkg.LPkg.Module().NamedFunction("runtime.semaAcquireWaitGroup$coro")
		if !function.IsNil() && function.BasicBlocksCount() != 0 {
			body = function.String()
		}
	}
	packages, err := Do([]string{"file=" + source}, conf)
	if err != nil {
		t.Fatalf("build WaitGroup fixture: %v", err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil {
		t.Fatal("production pipeline did not return its program")
	}
	defer packages[0].LPkg.Prog.Dispose()
	if !strings.Contains(body, "alloca %runtime.llgoCoroSemaphoreParkV2") || strings.Contains(body, "AllocZ") {
		t.Fatalf("production semaphore state is not coroutine-frame-owned:\n%s", body)
	}
	for _, call := range []string{
		"call void @\"" + coroSemaphorePrepareOrAbortSymbolV3 + "$outcome\"(",
		"call void @" + coroKeyedParkSymbolV2 + "(",
		"call i32 @" + coroKeyedResumeSymbolV2 + "(",
	} {
		if strings.Count(body, call) != 1 {
			t.Errorf("semaphore body must contain exactly one %s call:\n%s", call, body)
		}
	}
}
