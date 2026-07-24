//go:build !llgo

package build

import (
	"errors"
	"fmt"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This is an integration regression for the real runtime closure, not a probe.
// The terminal legacy panic path deliberately uses printanyraw: tracing an
// already-unhandled panic must not dynamically invoke Error or String after
// the native unwinder has abandoned the managed path. Consequently the whole
// Panic -> Rethrow -> TracePanic chain is now a certifiable plain island.
func TestRealRuntimeLegacyPanicPlainCertificateAcceptsRawTerminalTrace(t *testing.T) {
	sentinel := errors.New("legacy raw panic trace verified")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			return nil, err
		}
		if err := validateCoroUnwindOnlyLoweredCalls(plan, coro.PanicExplicitStatusABIV0); err != nil {
			return nil, fmt.Errorf("real runtime raw terminal panic trace is not a plain certificate: %w", err)
		}
		functions := make(map[string]*ssa.Function)
		for _, name := range []string{"Panic", "Rethrow", "TracePanic", "printanyraw", "printany"} {
			function, findErr := findUniqueCoroWorkerPlanFunction(input.Program, llssa.PkgRuntime, name)
			if findErr != nil {
				return nil, findErr
			}
			functions[name] = function
		}
		direct := func(owner, target string) bool {
			for _, call := range coroPlanTestCalls(functions[owner]) {
				if call.Common() != nil && call.Common().StaticCallee() == functions[target] {
					return true
				}
			}
			return false
		}
		for _, edge := range [][2]string{{"Panic", "Rethrow"}, {"Rethrow", "TracePanic"}, {"TracePanic", "printanyraw"}} {
			if !direct(edge[0], edge[1]) {
				return nil, fmt.Errorf("real runtime raw terminal panic trace lacks direct %s -> %s edge", edge[0], edge[1])
			}
		}
		if direct("TracePanic", "printany") {
			return nil, fmt.Errorf("real runtime terminal TracePanic still calls callback-capable printany")
		}
		return nil, sentinel
	}
	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want verified raw terminal panic trace", err)
	}
}
