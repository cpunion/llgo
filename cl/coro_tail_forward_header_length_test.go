//go:build !llgo

package cl

import (
	"go/constant"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
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
		target                    *llssa.Target
		intIR                     string
	}{
		{"slice", "[]byte", "value", nil, ""},
		{"string", "string", "value", nil, ""},
		{"field", "struct { Prefix int; Data []byte }", "value.Data", nil, ""},
		{"wasm32-slice", "[]byte", "value", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, "i32"},
		{"wasm32-string", "string", "value", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, "i32"},
		{"wasm32-field", "struct { Prefix int; Data []byte }", "value.Data", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, "i32"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nfunc Child(length int) int { return length }\n" +
				"func Parent(value " + test.parameter + ") int { return Child(len(" + test.argument + ")) }\n"
			prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABISource(t, test.target, source)
			defer prog.Dispose()
			wantIntIR := test.intIR
			if wantIntIR == "" {
				wantIntIR = "i" + strconv.Itoa(prog.PointerSize()*8)
			}
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
			argumentWidth := regexp.MustCompile(`call ptr @"?foo\.Child\$coro"?\(ptr [^,]+, ptr [^,]+, ` + wantIntIR + ` [^)]*\)`)
			if !argumentWidth.MatchString(body) {
				t.Fatalf("header length does not use target int width %s:\n%s", wantIntIR, body)
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

func TestCoroHeaderLengthTailForwardKeepsBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, result, child, directive string
	}{
		{"noinline", "int", "return length", "//go:noinline\n"},
		{"recover", "any", "return recover()", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nfunc Child(length int) " + test.result + " { " + test.child + " }\n" +
				test.directive + "func Parent(value []byte) " + test.result + " { return Child(len(value)) }\n"
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
			if err != nil || physical.tailForward != nil {
				t.Fatalf("%s boundary acquired a frame-free recipe: %+v, error=%v", test.name, physical, err)
			}
			if body := requireCoroPhysicalFunction(t, module, "foo.Parent").String(); !strings.Contains(body, coroFrameAllocHookV1) {
				t.Fatalf("%s boundary lost its physical frame:\n%s", test.name, body)
			}
		})
	}
}
