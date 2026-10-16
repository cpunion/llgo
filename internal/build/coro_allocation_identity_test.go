//go:build !llgo

package build

import (
	"strings"
	"testing"
)

func TestCoroRepeatedAllocationIdentityCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	bodies := make(map[string]string)
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil {
			return
		}
		for _, name := range []string{"ArrayHistory", "SelectHistory", "PhiHistory", "ImmediateBorrow"} {
			for _, suffix := range []string{"$coro", ""} {
				function := pkg.LPkg.Module().NamedFunction("main." + name + suffix)
				if !function.IsNil() && function.BasicBlocksCount() != 0 {
					bodies[name] += function.String()
				}
			}
		}
	}
	packages, err := Do([]string{"./_testgo/coro_allocation_identity"}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil {
		t.Fatal("complete pipeline did not return its program")
	}
	defer packages[0].LPkg.Prog.Dispose()
	for _, name := range []string{"ArrayHistory", "SelectHistory", "PhiHistory", "ImmediateBorrow"} {
		body := bodies[name]
		if body == "" {
			t.Fatalf("missing source body %s", name)
		}
		wantHeap := name != "ImmediateBorrow"
		if gotHeap := strings.Contains(body, "runtime.AllocZ"); gotHeap != wantHeap {
			t.Fatalf("%s dynamic heap allocation=%v, want %v:\n%s", name, gotHeap, wantHeap, body)
		}
	}
}
