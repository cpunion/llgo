package ssa

import (
	"fmt"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func intrinsicDeclarationTestPackage(t *testing.T, target *Target) Package {
	t.Helper()
	Initialize(InitAll)
	prog := NewProgram(target)
	pkg := prog.NewPackage("intrinsicdecl", "intrinsic/decl")
	t.Cleanup(func() { pkg.Module().Dispose(); prog.Dispose() })
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	function := pkg.NewFunc("caller", signature, InC)
	b := function.MakeBody(1)
	b.impl.CreateIntrinsic(prog.Void().ll, llvm.LookupIntrinsicID("llvm.trap"), nil, "")
	b.Unreachable()
	b.EndBuild()
	b.Dispose()
	return pkg
}

func TestLLVMIntrinsicDeclarationIsSharedWithPackageFunction(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			pkg := intrinsicDeclarationTestPackage(t, target.arch)
			existing := pkg.Module().NamedFunction("llvm.trap")
			if existing.IsNil() || existing.IntrinsicID() == 0 || pkg.FuncOf("llvm.trap") != nil {
				t.Fatal("fixture did not create the intrinsic outside the package symbol table")
			}
			function := pkg.NewFunc("llvm.trap", types.NewSignatureType(nil, nil, nil, nil, nil, false), InC)
			if function.impl.C != existing.C || pkg.FuncOf("llvm.trap") != function {
				t.Fatal("package declaration did not adopt the exact LLVM intrinsic")
			}
			if strings.Contains(pkg.String(), "@llvm.trap.") {
				t.Fatal("package declaration created a renamed intrinsic")
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, test := range []struct {
		name string
		sig  *types.Signature
		bg   Background
	}{
		{"signature", types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewParam(0, nil, "", types.Typ[types.Uint32])), nil, false), InC},
		{"calling-convention", types.NewSignatureType(nil, nil, nil, nil, nil, false), InGo},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg := intrinsicDeclarationTestPackage(t, nil)
			defer func() {
				if got := fmt.Sprint(recover()); !strings.Contains(got, "conflicting LLVM intrinsic declaration") {
					t.Fatalf("invalid intrinsic declaration panic=%s", got)
				}
			}()
			pkg.NewFunc("llvm.trap", test.sig, test.bg)
		})
	}
}
