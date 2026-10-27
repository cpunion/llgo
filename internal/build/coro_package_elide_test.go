package build

import (
	"go/types"
	"regexp"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// Exercise the explicit package lowering pipeline, which runs before the
// requested optimizer. Once CoroCleanup has erased coro.id, a later default
// pipeline cannot recover the child's bounded lifetime proof.
func TestCoroPackageLoweringElidesCertifiedStaticChild(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, platform := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		for _, certified := range []bool{false, true} {
			name := platform.name + "/ordinary"
			if certified {
				name = platform.name + "/certified"
			}
			t.Run(name, func(t *testing.T) {
				prog := llssa.NewProgram(platform.target)
				defer prog.Dispose()
				pkg := prog.NewPackage("elide", "elide")
				mod := pkg.Module()
				defer mod.Dispose()
				mod.SetDataLayout(prog.DataLayout())
				mod.SetTarget(prog.TargetSpec().Triple)
				pointer := types.Typ[types.UnsafePointer]
				word := types.Typ[types.Uintptr]
				publish := pkg.NewFunc("elide_child_publish", newSignature([]types.Type{pointer, pointer}, nil), llssa.InC)
				makeFrame := func(prefix string) llssa.CoroOptions {
					alloc := pkg.NewFunc(prefix+"_alloc", newSignature([]types.Type{word, word}, []types.Type{pointer}), llssa.InC)
					free := pkg.NewFunc(prefix+"_free", newSignature([]types.Type{pointer, word, word}, nil), llssa.InC)
					return llssa.CoroOptions{AllocationAlign: 32, Frame: llssa.CoroFrameOps{
						Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr { return b.Call(alloc.Expr, size, align) },
						Free:  func(b llssa.Builder, frame, size, align llssa.Expr) { b.Call(free.Expr, frame, size, align) },
					}}
				}
				child := pkg.NewFunc("elide_child", newSignature(nil, []types.Type{pointer}), llssa.InGo)
				cb := child.MakeBody(1)
				options := makeFrame("elide_child")
				options.BeforeInitialSuspend = func(b llssa.Builder, handle, storage llssa.Expr) { b.Call(publish.Expr, handle, storage) }
				cc := cb.BeginCoro(options)
				cc.Finish()
				cb.EndBuild()
				cb.Dispose()
				parent := pkg.NewFunc("elide_parent", newSignature(nil, []types.Type{pointer}), llssa.InGo)
				pb := parent.MakeBody(1)
				pc := pb.BeginCoro(makeFrame("elide_parent"))
				handle := pb.Call(child.Expr)
				if certified && !pb.MarkCoroElideSafe(handle) {
					t.Fatal("missing LLVM elision attribute")
				}
				pb.CoroResume(handle)
				// The child remains live across a parent suspension. Its borrowed
				// storage must be part of the parent frame, not a native activation.
				pc.Suspend()
				pb.CoroDestroy(handle)
				pc.Finish()
				pb.EndBuild()
				pb.Dispose()
				if err := lowerCoroPackageModuleWithProgram(prog, "elide", mod); err != nil {
					t.Fatal(err)
				}
				resume := mod.NamedFunction("elide_parent.resume").String()
				callsChild := strings.Contains(resume, "call ptr @elide_child()")
				if callsChild == certified {
					t.Fatalf("certified=%t retained dynamic child ramp=%t:\n%s", certified, callsChild, resume)
				}
				if certified {
					if strings.Contains(resume, "@elide_child_alloc") || strings.Contains(resume, "@elide_child_free") {
						t.Fatalf("certified child retained allocation/free:\n%s", resume)
					}
					if !regexp.MustCompile(`call void @elide_child_publish\(ptr [^,]+, ptr null\)`).MatchString(resume) {
						t.Fatalf("certified child did not publish borrowed storage:\n%s", resume)
					}
					if !strings.Contains(resume, "%elide_parent.Frame") {
						t.Fatalf("missing suspended parent frame:\n%s", resume)
					}
				}
				if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
