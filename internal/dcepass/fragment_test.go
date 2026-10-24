package dcepass

import (
	"path/filepath"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestMethodFragmentOutlivesSourceContext(t *testing.T) {
	srcCtx, fragmentCtx, dstCtx := llvm.NewContext(), llvm.NewContext(), llvm.NewContext()
	defer fragmentCtx.Dispose()
	defer dstCtx.Dispose()
	src := parseModule(t, &srcCtx, filepath.Join("testdata", "method_slots", "in.ll"))
	fragment := fragmentCtx.NewModule("fragment")
	defer fragment.Dispose()
	if skipped := CaptureMethodTypes(fragment, src); len(skipped) != 0 {
		t.Fatal(skipped)
	}
	src.Dispose()
	srcCtx.Dispose()
	if err := llvm.VerifyModule(fragment, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	for fn := fragment.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if !fn.IsDeclaration() {
			t.Fatalf("fragment retained function body %s", fn.Name())
		}
	}
	dst := dstCtx.NewModule("entry")
	defer dst.Dispose()
	seen := make(map[string]bool)
	EmitMethodTypeFragment(dst, fragment, map[string][]int{taskTypeName: {1}}, seen, false)
	if err := llvm.VerifyModule(dst, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	methods, _, ok := methodArray(dst.NamedGlobal(taskTypeName).Initializer())
	if !ok || methods.Operand(0).Operand(2).Name() != unreachableMethodName || methods.Operand(1).Operand(2).Name() != "main.(*Task).Run" {
		t.Fatal("captured methods were not pruned by the live slot summary")
	}
	// An earlier unsupported definition blocks a later otherwise safe copy.
	blocked := dstCtx.NewModule("blocked")
	defer blocked.Dispose()
	EmitMethodTypeFragment(blocked, fragment, nil, map[string]bool{taskTypeName: true}, false)
	if g := blocked.NamedGlobal(taskTypeName); !g.IsNil() && !g.Initializer().IsNil() {
		t.Fatal("overrode an earlier unsupported definition")
	}
}

func TestMethodFragmentRejectsLocalFunctionDependencies(t *testing.T) {
	ctx, fragmentCtx := llvm.NewContext(), llvm.NewContext()
	defer ctx.Dispose()
	defer fragmentCtx.Dispose()
	src := parseModule(t, &ctx, filepath.Join("testdata", "method_slots", "in.ll"))
	defer src.Dispose()
	fn := src.NamedFunction("main.(*Task).Run")
	fn.SetLinkage(llvm.InternalLinkage)
	block := ctx.AddBasicBlock(fn, "entry")
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(block)
	builder.CreateRet(llvm.ConstInt(ctx.Int64Type(), 0, false))
	fragment := fragmentCtx.NewModule("fragment")
	defer fragment.Dispose()
	skipped := CaptureMethodTypes(fragment, src)
	if len(skipped) != 2 {
		t.Fatalf("unsafe table definitions skipped: %v", skipped)
	}
	if !fragment.NamedFunction(fn.Name()).IsNil() {
		t.Fatal("package-local function reference escaped into fragment")
	}
	if err := llvm.VerifyModule(fragment, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
