package build

import (
	"go/types"
	"os"
	"path/filepath"
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
		name   string
		target *llssa.Target
		root   string
	}{
		{name: "native"},
		{name: "native-llvm-used", root: "llvm.used"},
		{name: "native-compiler-used", root: "llvm.compiler.used"},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog := llssa.NewProgram(target.target)
			defer func() { prog.Dispose() }()
			pkg := prog.NewPackage("owner", "owner")
			mod := pkg.Module()
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

			if err := lowerCoroPackageModuleWithProgram(prog, "owner", mod); err != nil {
				t.Fatal(err)
			}
			resume := mod.NamedFunction("suspended_owner.resume")
			if resume.IsNil() {
				t.Fatal("missing resume body")
			}
			if !strings.Contains(resume.String(), "worker.owner.local.reload.addr = getelementptr inbounds %suspended_owner.Frame") {
				t.Fatalf("resumed owner address is not in the coroutine frame:\n%s", resume.String())
			}
			if strings.Contains(mod.String(), "call void (...) @llvm.fake.use") {
				t.Fatal("post-split owner fake use was not removed")
			}
			cleanup := mod.NamedFunction("suspended_owner.cleanup")
			if cleanup.IsNil() {
				t.Fatal("fixture has no cleanup clone before preparation")
			}
			if target.root != "" {
				// Metadata-only roots must survive, even for an otherwise dead clone.
				init := llvm.ConstArray(cleanup.Type(), []llvm.Value{cleanup})
				used := llvm.AddGlobal(mod, init.Type(), target.root)
				used.SetInitializer(init)
				used.SetLinkage(llvm.AppendingLinkage)
				used.SetSection("llvm.metadata")
			}
			if err := prepareLoweredCoroPackageForOptimization(prog, "owner", mod); err != nil {
				t.Fatal(err)
			}
			previous := prog
			var err error
			prog, mod, err = compactLoweredCoroPackage(prog, mod, "owner", false)
			if err != nil {
				t.Fatal(err)
			}
			if prog == previous || mod.DataLayout() != prog.DataLayout() {
				t.Fatal("compaction kept the old owner or changed target layout")
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			if got := !mod.NamedFunction("suspended_owner.cleanup").IsNil(); got != (target.root != "") {
				t.Fatalf("cleanup retained = %t, metadata root = %q", got, target.root)
			}
			resume = mod.NamedFunction("suspended_owner.resume")
			if resume.IsNil() || mod.NamedFunction("suspended_owner.destroy").IsNil() {
				t.Fatal("preparation removed a reachable resume/destroy body")
			}
			if !strings.Contains(resume.String(), "worker.owner.local.reload.addr = getelementptr inbounds %suspended_owner.Frame") {
				t.Fatalf("preparation lost the suspended owner:\n%s", resume.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			object.Dispose()
		})
	}
}

func TestStagedPackageCompactionWriteFailurePreservesOwner(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	mod := prog.NewPackage("owner", "owner").Module()
	bad := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(bad, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", bad)
	next, nextMod, err := compactLoweredCoroPackage(prog, mod, "owner", false)
	if err == nil || next != prog || nextMod.C != mod.C {
		t.Fatalf("failed serialization lost original owner: %v", err)
	}
	if err := llvm.VerifyModule(nextMod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
