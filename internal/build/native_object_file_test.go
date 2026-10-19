//go:build !llgo && cgo

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestStagedNativeObjectFileMatchesMemoryBuffer(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		spec *llssa.Target
	}{
		{name: "native"},
		{name: "wasm", spec: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog := llssa.NewProgram(target.spec)
			defer prog.Dispose()
			mod := prog.NewPackage("object", "object").Module()
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.TargetSpec().Triple)
			ctx := mod.Context()
			fn := llvm.AddFunction(mod, "answer", llvm.FunctionType(ctx.Int32Type(), nil, false))
			builder := ctx.NewBuilder()
			defer builder.Dispose()
			builder.SetInsertPointAtEnd(ctx.AddBasicBlock(fn, "entry"))
			builder.CreateRet(llvm.ConstInt(ctx.Int32Type(), 42, false))
			buffer, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			defer buffer.Dispose()
			name := filepath.Join(t.TempDir(), "answer.o")
			if err := emitNativeObjectFile(prog.TargetMachine(), mod, name); err != nil {
				t.Fatal(err)
			}
			object, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if len(object) == 0 || !bytes.Equal(object, buffer.Bytes()) {
				t.Fatal("file and memory-buffer emission produced different objects")
			}
			if err := emitNativeObjectFile(prog.TargetMachine(), mod, filepath.Join(name, "missing.o")); err == nil {
				t.Fatal("object emission to a non-directory unexpectedly succeeded")
			}
		})
	}
}
