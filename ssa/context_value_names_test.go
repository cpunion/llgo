package ssa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestDiscardLocalValueNamesPreservesSymbolsAndRoots(t *testing.T) {
	Initialize(InitAll)
	for _, target := range []struct {
		name string
		spec *Target
	}{{name: "native"}, {name: "wasm", spec: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			prog := NewProgram(target.spec)
			defer prog.Dispose()
			mod := prog.NewPackage("names", "names").Module()
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.TargetSpec().Triple)
			ctx := mod.Context()
			i32 := ctx.Int32Type()
			fn := llvm.AddFunction(mod, "public_answer", llvm.FunctionType(i32, []llvm.Type{i32}, false))
			fn.Param(0).SetName("argument")
			b := ctx.NewBuilder()
			defer b.Dispose()
			b.SetInsertPointAtEnd(ctx.AddBasicBlock(fn, "entry"))
			sum := b.CreateAdd(fn.Param(0), llvm.ConstInt(i32, 1, false), "result")
			b.CreateRet(sum)
			owner := llvm.AddGlobal(mod, i32, "retained.owner")
			owner.SetInitializer(llvm.ConstInt(i32, 42, false))
			owner.SetLinkage(llvm.PrivateLinkage)
			init := llvm.ConstArray(fn.Type(), []llvm.Value{fn, owner})
			used := llvm.AddGlobal(mod, init.Type(), "llvm.used")
			used.SetInitializer(init)
			used.SetLinkage(llvm.AppendingLinkage)
			used.SetSection("llvm.metadata")
			if fn.Param(0).Name() != "argument" || sum.Name() != "result" {
				t.Fatal("source fixture does not retain local names")
			}
			file, err := os.Create(filepath.Join(t.TempDir(), "names.bc"))
			if err != nil {
				t.Fatal(err)
			}
			if err := llvm.WriteBitcodeToFile(mod, file); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			loaded := NewProgram(target.spec)
			defer loaded.Dispose()
			loaded.DiscardLocalValueNames()
			parsed, err := loaded.ParseBitcodeFile(file.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := llvm.VerifyModule(parsed, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			answer := parsed.NamedFunction("public_answer")
			if answer.IsNil() || answer.Param(0).Name() != "" || answer.FirstBasicBlock().FirstInstruction().Name() != "" {
				t.Fatal("staged module retained local names or lost its global entry")
			}
			retained := parsed.NamedGlobal("retained.owner")
			root := parsed.NamedGlobal("llvm.used")
			if retained.IsNil() || retained.Linkage() != llvm.PrivateLinkage || root.IsNil() || !strings.Contains(root.String(), "@public_answer") || !strings.Contains(root.String(), "@retained.owner") {
				t.Fatal("global symbol or metadata root changed")
			}
			if parsed.DataLayout() != mod.DataLayout() || parsed.Target() != mod.Target() {
				t.Fatal("target layout changed")
			}
			object, err := loaded.TargetMachine().EmitToMemoryBuffer(parsed, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			object.Dispose()
		})
	}
}
