//go:build go1.27 && !llgo

package cl

import (
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestCoroExactGenericMethodWrappers(t *testing.T) {
	const source = `package foo
type S[A any] struct{ value A }
type E[A any] struct{ S[A] }
type P[A any] struct{ *S[A] }
func (s S[A]) M[B any](b B) (A, B) { return s.value, b }
func (s *S[A]) Pointer[B any](b B) (A, B) { return s.value, b }
func (s S[A]) Unused[B any]() A { return s.value }
func (s S[A]) Slice[B ~[]A](b B) A { return b[0] }
func (s S[A]) Variadic[B any](b ...B) B { return b[0] }
func Ordinary[B any](b B) (int, B) { return 0, b }
func Expression[A, B any]() func(S[A], B) (A, B) { return S[A].M[B] }
func Bound[A, B any](s S[A]) func(B) (A, B) { return s.M[B] }
func Promoted[A, B any]() func(E[A], B) (A, B) { return E[A].M[B] }
func Pointer[A, B any]() func(P[A], B) (A, B) { return P[A].Pointer[B] }
var Keep any
func Root() {
	_, _ = Ordinary[string]("canonical signature collision")
	_ = Expression[int, string]()
	_ = Bound[int, string](S[int]{})
	_ = Promoted[int, string]()
	_ = Pointer[int, string]()
	Keep = (*S[int]).M[string]
	Keep = S[int].Slice[[]int]
	Keep = S[int].Variadic[string]
	Keep = S[int].Unused[string]
	Keep = S[int].Unused[bool]
	Keep = S[int]{}.Unused[string]
	Keep = S[int]{}.Unused[bool]
}
`
	pkg, _, _ := buildGoSSAPkg(t, source)
	wrappers := make(map[string]*ssa.Function)
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn == nil || !coroGroundGenericTypeArgs(fn.TypeArgs()) {
			continue
		}
		var validate func(*ssa.Function) error
		switch {
		case strings.HasPrefix(fn.Synthetic, "thunk for "):
			validate = validateCoroExactMethodExpressionThunk
		case strings.HasPrefix(fn.Synthetic, "bound method wrapper for "):
			validate = validateCoroExactBoundMethodWrapper
		default:
			continue
		}
		if err := validate(fn); err != nil {
			t.Fatalf("%s rejected: %v; signature=%v object=%v args=%v", fn, err, fn.Signature, fn.Object(), fn.TypeArgs())
		}
		plain := coro.FunctionPlan{FuncRep: coro.Dispatch, Emission: coro.EmitPlain, Primary: coro.PrimaryPlain}
		if err := validateCoroDynamicDispatchTarget(fn, plain); err != nil {
			t.Fatalf("%s has no exact instantiated dispatch ABI: %v", fn, err)
		}
		wrappers[fn.Name()] = fn
		original := fn.Synthetic
		fn.Synthetic += " forged"
		if err := validate(fn); err == nil {
			t.Fatalf("%s accepted a forged identity", fn)
		}
		fn.Synthetic = original
	}
	for _, name := range []string{"M[string]$thunk", "M[string]$bound", "Pointer[string]$thunk", "Slice[[]int]$thunk", "Variadic[string]$thunk"} {
		if wrappers[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	for _, suffix := range []string{"$thunk", "$bound"} {
		left, right := wrappers["Unused[string]"+suffix], wrappers["Unused[bool]"+suffix]
		if left == nil || right == nil {
			t.Fatalf("missing unused-type-argument wrappers for %s", suffix)
		}
		findCall := func(fn *ssa.Function) *ssa.Call {
			for _, instruction := range fn.Blocks[0].Instrs {
				if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee() != nil {
					return call
				}
			}
			t.Fatalf("%s has no static method call", fn)
			return nil
		}
		leftCall, rightCall := findCall(left), findCall(right)
		if !types.Identical(leftCall.Common().Signature(), rightCall.Common().Signature()) {
			t.Fatal("negative fixture does not have identical physical signatures")
		}
		validate := validateCoroExactMethodExpressionThunk
		if suffix == "$bound" {
			validate = validateCoroExactBoundMethodWrapper
		}
		original := leftCall.Call.Value
		leftCall.Call.Value = rightCall.Call.Value
		if err := validate(left); err == nil {
			t.Fatalf("%s accepted a different same-signature method instantiation", left)
		}
		leftCall.Call.Value = original
	}
	thunk := wrappers["M[string]$thunk"]
	object := thunk.Object().(*types.Func)
	declared := object.Type().(*types.Signature)
	withoutTypeParams := types.NewFunc(object.Pos(), object.Pkg(), object.Name(),
		types.NewSignatureType(declared.Recv(), nil, nil, declared.Params(), declared.Results(), declared.Variadic()))
	if _, _, err := coroExactWrapperMethod(thunk, withoutTypeParams); err == nil {
		t.Fatal("method arguments without matching declaration parameters were accepted")
	}
}
