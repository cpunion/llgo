//go:build !llgo

package cabi

import (
	"fmt"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// ABI carriers can require more alignment and storage than the source value.
// Neither property grants permission to widen or strengthen a source read.
func TestAggregateReturnLoadSafety(t *testing.T) {
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	llvm.InitializeAllTargetInfos()
	for _, target := range []struct{ goos, arch, triple string }{
		{"darwin", "arm64", "arm64-apple-darwin"},
		{"linux", "amd64", "x86_64-unknown-linux-gnu"},
	} {
		for _, size := range []int{2, 3, 5, 7, 8, 9, 12, 15, 16} {
			for _, volatile := range []bool{false, true} {
				for _, optimize := range []bool{false, true} {
					name := fmt.Sprintf("%s/bytes%d/volatile%t/optimize%t", target.arch, size, volatile, optimize)
					t.Run(name, func(t *testing.T) {
						prog := llssa.NewProgram(&llssa.Target{GOOS: target.goos, GOARCH: target.arch})
						defer prog.Dispose()
						ctx := llvm.NewContext()
						defer ctx.Dispose()
						m := ctx.NewModule("return_load")
						defer m.Dispose()
						m.SetTarget(target.triple)
						td := prog.TargetData()
						m.SetDataLayout(td.String())
						typ := llvm.ArrayType(ctx.Int8Type(), size)
						fn := llvm.AddFunction(m, "read_value", llvm.FunctionType(typ, []llvm.Type{llvm.PointerType(typ, 0)}, false))
						b := ctx.NewBuilder()
						defer b.Dispose()
						b.SetInsertPointAtEnd(ctx.AddBasicBlock(fn, "entry"))
						load := b.CreateLoad(typ, fn.Param(0), "captured")
						load.SetAlignment(1)
						load.SetVolatile(volatile)
						b.CreateRet(load)
						if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err != nil {
							t.Fatal(err)
						}
						NewTransformer(prog, target.triple, "", ModeAllFunc, optimize).TransformModule("test", m)
						if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err != nil {
							t.Fatal(err, m.String())
						}
						fn = m.NamedFunction("read_value")
						sourceReads := 0
						for inst := fn.EntryBasicBlock().FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
							if inst.IsALoadInst().IsNil() {
								continue
							}
							ptr := inst.Operand(0)
							if ptr == fn.Param(0) {
								sourceReads++
								if inst.Alignment() > 1 {
									t.Errorf("source load alignment strengthened: %s", inst.String())
								}
								if td.TypeStoreSize(inst.Type()) > uint64(size) {
									t.Errorf("source load exceeds logical storage: %s", inst.String())
								}
								if volatile && !inst.IsVolatile() {
									t.Errorf("volatile snapshot replaced by ordinary source read: %s", inst.String())
								}
							} else if !ptr.IsAAllocaInst().IsNil() {
								if td.TypeStoreSize(inst.Type()) > td.TypeAllocSize(ptr.AllocatedType()) {
									t.Errorf("carrier load exceeds return storage: %s from %s", inst.String(), ptr.String())
								}
								if inst.Alignment() > ptr.Alignment() {
									t.Errorf("carrier load alignment exceeds return storage: %s from %s", inst.String(), ptr.String())
								}
							}
						}
						if volatile && sourceReads != 1 {
							t.Errorf("volatile return reads source %d times, want one", sourceReads)
						}
					})
				}
			}
		}
	}
}
