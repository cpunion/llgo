package build

import (
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// The SCC-local CFG cleanup runs before the module-wide fake-use removal.
// An owner referenced only by KeepAlive after suspension must still occupy
// the coroutine frame, including when its address crosses a resume branch.
func TestCoroPackageLoweringPreservesSuspendedOwner(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name      string
		target    *llssa.Target
		optimized bool
	}{
		{name: "native"},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
		{name: "native optimized", optimized: true},
		{name: "wasm optimized", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, optimized: true},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog := llssa.NewProgram(target.target)
			defer prog.Dispose()
			pkg := prog.NewPackage("owner", "owner")
			mod := pkg.Module()
			defer mod.Dispose()
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.TargetSpec().Triple)
			pointer := types.Typ[types.UnsafePointer]
			uintptrType := types.Typ[types.Uintptr]
			alloc := pkg.NewFunc("frame_alloc", newSignature(
				[]types.Type{uintptrType, uintptrType}, []types.Type{pointer},
			), llssa.InC)
			free := pkg.NewFunc("frame_free", newSignature(
				[]types.Type{pointer, uintptrType, uintptrType}, nil,
			), llssa.InC)
			fn := pkg.NewFunc("suspended_owner", newSignature(nil, []types.Type{pointer}), llssa.InGo)
			b := fn.MakeBody(1)
			coro := b.BeginCoro(llssa.CoroOptions{Frame: llssa.CoroFrameOps{
				Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr {
					return b.Call(alloc.Expr, size, align)
				},
				Free: func(b llssa.Builder, frame, size, align llssa.Expr) {
					b.Call(free.Expr, frame, size, align)
				},
			}})
			owner := b.AllocaT(prog.Uint64()).SetName("worker.owner.local")
			b.Store(owner, prog.IntVal(42, prog.Uint64()))
			coro.Suspend()
			b.KeepAlive(owner)
			coro.Finish()
			b.EndBuild()
			b.Dispose()

			pipeline := coroPackageLoweringPipeline
			if target.optimized {
				pipeline = "default<Os>"
			}
			if err := lowerCoroPackageModuleWithPipeline(prog, "owner", mod, pipeline, target.optimized); err != nil {
				t.Fatal(err)
			}
			resume := mod.NamedFunction("suspended_owner.resume")
			if resume.IsNil() {
				t.Fatal("missing resume body")
			}
			var ownerAddress llvm.Value
			for bb := resume.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
				for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
					if inst.Name() == "worker.owner.local.reload.addr" {
						ownerAddress = inst
					}
				}
			}
			if ownerAddress.IsNil() || ownerAddress.InstructionOpcode() != llvm.GetElementPtr ||
				ownerAddress.Operand(0).C != resume.Param(0).C {
				t.Fatalf("resumed owner address is not in the coroutine frame:\n%s", resume.String())
			}
			if strings.Contains(mod.String(), "call void (...) @llvm.fake.use") {
				t.Fatal("post-split owner fake use was not removed")
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			object.Dispose()
		})
	}
}
