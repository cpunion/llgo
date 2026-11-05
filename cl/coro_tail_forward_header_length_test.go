//go:build !llgo

package cl

import (
	"go/constant"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

func TestCoroHeaderLengthForwardingSourceProof(t *testing.T) {
	const source = `package foo
type Bytes []byte
type Text string
type Header struct { Prefix int; Value struct { Bytes Bytes; Text Text } }
func Child(value int) int { return value }
func Slice(value []byte) int { return Child(len(value)) }
func String(value string) int { return Child(len(value)) }
func Named(value Bytes) int { return Child(len(value)) }
func Field(value Header) int { return Child(len(value.Value.Bytes)) }
func TextField(value Header) int { return Child(len(value.Value.Text)) }
func Map(value map[int]int) int { return Child(len(value)) }
func Channel(value chan int) int { return Child(len(value)) }
func Pointer(value *[]byte) int { return Child(len(*value)) }
func Changed(value []byte) int { return Child(len(value))+1 }
func SideEffect(value []byte) int { return Child(len(append(value, 1))) }
`
	pkg, _, _ := buildGoSSAPkg(t, source)
	for _, name := range []string{"Slice", "String", "Named", "Field", "TextField"} {
		shape := deriveCoroForwardingSourceShape(pkg.Func(name))
		if shape == nil || shape.target != pkg.Func("Child") || len(shape.args) != 1 {
			t.Fatalf("%s lost its exact header-length source proof: %+v", name, shape)
		}
		argument := shape.args[0]
		if !argument.headerLength || argument.direct || argument.sourceParameter != 0 {
			t.Fatalf("%s header argument = %+v", name, argument)
		}
		if strings.HasSuffix(name, "Field") && len(argument.fieldPath) != 2 {
			t.Fatalf("%s lost its evaluated value-field path: %+v", name, argument)
		}
		if _, _, ok := composeCoroDeliveryForwardingArguments(shape, coroSourceDeliveryRecipe{}); ok {
			t.Fatalf("%s incorrectly transported a header through a scalar delivery view", name)
		}
	}
	for _, name := range []string{"Map", "Channel", "Pointer", "Changed", "SideEffect"} {
		if shape := deriveCoroForwardingSourceShape(pkg.Func(name)); shape != nil {
			t.Fatalf("%s acquired an unsupported pure header projection: %+v", name, shape)
		}
	}
}

func TestCoroHeaderLengthTailForwardReusesTargetFrame(t *testing.T) {
	for _, test := range []struct {
		name, parameter, argument string
	}{
		{"slice", "[]byte", "value"},
		{"string", "string", "value"},
		{"field", "struct { Prefix int; Data []byte }", "value.Data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nfunc Child(length int) int { return length }\n" +
				"func Parent(value " + test.parameter + ") int { return Child(len(" + test.argument + ")) }\n"
			prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABISource(t, nil, source)
			defer prog.Dispose()
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			parent := ssaPkg.Func("Parent")
			physical, err := universe.coroProgramIR.physicalFunctionPlan(parent, universe.ownerOf(parent))
			if err != nil || physical.tailForward == nil || !physical.tailForward.args[0].headerLength {
				t.Fatalf("header-length physical plan = %+v, error=%v", physical, err)
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			for _, forbidden := range []string{"llvm.coro.", coroFrameAllocHookV1, coroAwaitPrepareInlineHookV4} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("header-length wrapper retained %s:\n%s", forbidden, body)
				}
			}
			if !strings.Contains(body, "extractvalue") || strings.Count(body, "call ptr") != 1 {
				t.Fatalf("wrapper is not one header projection and target call:\n%s", body)
			}
			// A corrupted frozen recipe must not turn an ordinary integer
			// parameter or constant into a header read.
			bad := *physical.tailForward
			bad.args = append([]coroPhysicalTailForwardArgument(nil), bad.args...)
			bad.args[0].sourceParameter = -1
			bad.args[0].constant = ssa.NewConst(constant.MakeInt64(0), parent.Signature.Results().At(0).Type())
			if err := bad.validate(parent, plan); err == nil {
				t.Fatal("header-length projection of a constant was accepted")
			}
			runCoroABITestPipeline(t, prog, module)
			if !module.NamedFunction("foo.Parent$coro.resume").IsNil() ||
				!module.NamedFunction("foo.Parent$coro.destroy").IsNil() {
				t.Fatal("header-only forwarding ramp acquired a frame lifetime")
			}
		})
	}
}
