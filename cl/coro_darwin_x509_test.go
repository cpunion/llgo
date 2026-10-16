//go:build !llgo

package cl

import (
	"os"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
)

func TestCoroDarwinX509UsesExistingTypedForeignBoundary(t *testing.T) {
	source, err := os.ReadFile("../runtime/internal/lib/runtime/syscall_darwin_x509_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	fixtureSource := strings.Replace(string(source), "package runtime", "package foreignworker", 1)
	fixtureSource = strings.Replace(fixtureSource, "func darwinX509Syscall(", "func Root(", 1)
	fixture := prepareCoroWorkerForeignFixture(t, fixtureSource, "Root")
	defer fixture.prog.Dispose()
	shape, found, err := validateCoroWorkerForeignCall(fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize())
	if err != nil || !found || shape.calleeType == nil || shape.argc != 6 || shape.mode != coroForeignCallModeWorker {
		t.Fatalf("Darwin x509 did not use the existing typed worker: %+v found=%t error=%v", shape, found, err)
	}
	compilation := &Compilation{
		CoroPlan: fixture.plan, EmissionUniverse: fixture.universe,
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV2,
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files,
		goembed.VarMap{}, PackageOptions{Compilation: compilation})
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	body := requireCoroPhysicalFunction(t, module, "foreignworker.Root")
	if countCoroIRDirectCalls(body, coroWorkerParkHookV1) != 1 || countCoroIRDirectCalls(body, coroWorkerResumeHookV1) != 1 {
		t.Fatalf("Darwin x509 bypassed the worker park/join protocol:\n%s", body.String())
	}
	calls := 0
	for fn := module.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if !strings.HasPrefix(fn.Name(), coroWorkerForeignThunkPrefixV1) {
			continue
		}
		for _, block := range fn.BasicBlocks() {
			for call := block.FirstInstruction(); !call.IsNil(); call = llvm.NextInstruction(call) {
				if call.InstructionOpcode() != llvm.Call || !call.CalledValue().IsAFunction().IsNil() {
					continue
				}
				calls++
				if call.OperandsCount() != 7 || call.Operand(5).Type().TypeKind() != llvm.DoubleTypeKind {
					t.Fatalf("FP argument lost its C register class:\n%s", call.String())
				}
			}
		}
	}
	if calls != 1 {
		t.Fatalf("typed raw-C calls=%d, want one", calls)
	}
	runCoroABITestPipeline(t, fixture.prog, module)
}
