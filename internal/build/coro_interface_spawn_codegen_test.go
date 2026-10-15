//go:build !llgo

package build

import (
	"strings"
	"testing"
)

// Partial-SSA spawn fixtures intentionally lack a complete runtime ABI. This
// real pipeline gate also freezes and verifies hidden receiver helper edges.
func TestCoroInterfaceSpawnCompleteRuntimeCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	var mainIR, deferIR string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil {
			return
		}
		for name, destination := range map[string]*string{
			"main.main$coro": &mainIR, "main.deferred$coro": &deferIR,
		} {
			fn := pkg.LPkg.Module().NamedFunction(name)
			if !fn.IsNil() && fn.BasicBlocksCount() != 0 {
				*destination = fn.String()
			}
		}
	}
	packages, err := Do([]string{"./_testgo/coro_interface_spawn"}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil {
		t.Fatal("complete pipeline did not return its program")
	}
	defer packages[0].LPkg.Prog.Dispose()
	for name, body := range map[string]string{"main": mainIR, "deferred": deferIR} {
		if !strings.Contains(body, "IfacePtrData") {
			t.Fatalf("%s lost its frozen source receiver normalization", name)
		}
	}
	if !strings.Contains(mainIR, "__llgo_coro_spawn_begin_v1") {
		t.Fatal("interface go did not enter the scheduler spawn transaction")
	}
}
