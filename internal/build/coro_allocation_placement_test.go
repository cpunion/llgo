//go:build !llgo

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Use the complete production runtime: an artificial source island cannot
// certify managed AllocZ or the exact instruction's helper observation.
func TestCoroUnpublishedAllocationPlacementIR(t *testing.T) {
	for _, test := range []struct{ name, tail string }{
		{"frozen no polls", ""},
		{"frozen preemption", "for i := 0; i < v; i++ { counter++ }"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testCoroUnpublishedAllocationPlacementIR(t, test.tail)
		})
	}
}

func testCoroUnpublishedAllocationPlacementIR(t *testing.T, tail string) {
	t.Helper()
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main
import "runtime"
var saved func() *int
var counter int
func child() *int { runtime.Gosched(); return new(int) }
func parent(v int) {
    var cell *int
    cell = child()
    saved = func() *int { return cell }
    `+tail+`
}
func main() { parent(0); if *saved() != 0 { panic("capture") } }
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
		function := pkg.LPkg.Module().NamedFunction("main.parent$coro")
		if !function.IsNil() && function.BasicBlocksCount() != 0 {
			body = function.String()
		}
	}
	packages, err := Do([]string{"file=" + source}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil {
		t.Fatal("missing production program")
	}
	defer packages[0].LPkg.Prog.Dispose()
	allocation := strings.Index(body, "runtime.AllocZ\"(i64 8)")
	consume := strings.Index(body, "@__llgo_coro_await_consume_v1")
	if allocation < 0 || consume < 0 || allocation < consume {
		t.Fatalf("heap capture cell was not allocated after child completion:\n%s", body)
	}
	if strings.Count(body, "runtime.AllocZ\"(i64 8)") != 1 {
		t.Fatalf("capture cell was not allocated exactly once:\n%s", body)
	}
	if !strings.Contains(body, "store volatile ptr null") {
		t.Fatalf("consumed pointer result remains rooted:\n%s", body)
	}
}
