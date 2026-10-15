//go:build !llgo

package build

import (
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// Observe the real production plan and emitted bodies: a wall-clock read is
// neither a worker transaction nor a request to activate the synctest service.
func TestCoroNativeTimeNowProductionPlanAndCodegen(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native clock gate requires Darwin or Linux")
	}
	const runtimePath = "github.com/xgo-dev/llgo/runtime/internal/runtime"
	targets := []struct{ path, name, symbol string }{
		{"time", "Now", "time.Now"},
		{"runtime", "time_runtimeNow", "time.runtimeNow"},
		{"runtime", "time_now", "time.now"},
		{"runtime", "nanotime1", "runtime.nanotime1"},
		{runtimePath, "SynctestNow", runtimePath + ".SynctestNow"},
	}
	var plan *coro.SSAPlan
	bodies := make(map[string]string)
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(next CoroPlanInput) (*coro.SSAPlan, error) {
		analyzed, err := defaultCoroPlanBuilder(next)
		if err == nil {
			plan = analyzed
		}
		return analyzed, err
	}
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil {
			return
		}
		for _, target := range targets {
			for _, suffix := range []string{"", "$outcome"} {
				physical := pkg.LPkg.Module().NamedFunction(target.symbol + suffix)
				if !physical.IsNil() && physical.BasicBlocksCount() != 0 {
					bodies[target.symbol] += physical.String()
				}
			}
		}
	}
	packages, err := Do([]string{"./_testgo/coro_time_now"}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil || plan == nil {
		t.Fatal("production pipeline did not return its program and plan")
	}
	defer packages[0].LPkg.Prog.Dispose()
	find := func(path, name string) *ssa.Function {
		t.Helper()
		var found *ssa.Function
		for _, record := range plan.Functions() {
			fn := record.Function
			if fn.Pkg == nil || llssa.PathOf(fn.Pkg.Pkg) != path || fn.Name() != name || record.Plan.Emission == coro.EmitNone {
				continue
			}
			if found != nil {
				t.Fatalf("multiple emitted canonical functions for %s.%s", path, name)
			}
			found = fn
		}
		if found == nil {
			t.Fatalf("no emitted canonical function for %s.%s", path, name)
		}
		return found
	}
	for _, target := range targets {
		path, name := target.path, target.name
		function := find(path, name)
		got, found := plan.FunctionPlan(function)
		// A synchronous status-returning body may transport panic without
		// requiring a coroutine frame. Its semantic representation is still
		// DirectCoro; the physical OutcomePlain entry is the relevant fact.
		plain := got.Emission == coro.EmitPlain && got.FuncRep == coro.DirectPlain ||
			got.Emission == coro.EmitOutcomePlain && got.ManagedEntry == coro.ManagedEntryOutcomePlain
		if !found || !plain || got.Effect&^coro.OutcomeStructured != coro.NoSuspend ||
			got.Exec&(coro.BlockForeign|coro.NeedsPreempt) != 0 {
			t.Fatalf("%s.%s retained a suspending clock path: %+v, found=%t", path, name, got, found)
		}
		body := bodies[target.symbol]
		if body == "" {
			t.Fatalf("%s.%s has no emitted synchronous body", path, name)
		}
		for _, forbidden := range []string{"$coro", "llvm.coro.", "__llgo_coro_worker_", "__llgo_coro_await_"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s.%s retained %s in its plain clock body", path, name, forbidden)
			}
		}
		if name == "SynctestNow" && strings.Contains(body, "currentSynctestBubble") {
			t.Error("disabled synctest clock retained its domain lookup")
		}
		if name == "nanotime1" && runtime.GOOS == "darwin" && !strings.Contains(body, "@clock_gettime_nsec_np(") {
			t.Error("Darwin monotonic clock did not emit its exact direct C call")
		}
	}
	if runtime.GOOS == "darwin" {
		clock := find("runtime", "c_clock_gettime_nsec_np")
		certificate, found := plan.CallableContractCertificate(clock)
		clockPlan, planned := plan.FunctionPlan(clock)
		if !found || !strings.HasPrefix(string(certificate.Contract.ID), "foreign.v1/") ||
			!coro.CallableContractDirectExecutorCompatible(certificate.Contract) || !planned ||
			clockPlan.External != coro.ExternalKnown || clockPlan.Effect != coro.NoSuspend || clockPlan.Exec != coro.IRQUnsafe {
			t.Fatalf("managed clock lost its exact nonblocking C contract: %+v/%+v", certificate, clockPlan)
		}
	}
}
