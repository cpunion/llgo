package cl

import (
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// An empty receiver identifies a package function. "numeric" groups official
// numeric vector methods; concrete shapes come from the resolved Go types.
type simdKey struct{ receiver, name string }
type simdSignature uint8

const (
	simdBinary simdSignature = iota
	simdExtract
	simdInsert
)

type simdOperation struct {
	op          llssa.SIMDOp
	signature   simdSignature
	integerOnly bool
}

var simdOperations = map[simdKey]simdOperation{
	{"numeric", "Add"}:     {llssa.SIMDAdd, simdBinary, false},
	{"numeric", "Sub"}:     {llssa.SIMDSub, simdBinary, false},
	{"numeric", "And"}:     {llssa.SIMDAnd, simdBinary, true},
	{"numeric", "Or"}:      {llssa.SIMDOr, simdBinary, true},
	{"numeric", "Xor"}:     {llssa.SIMDXor, simdBinary, true},
	{"numeric", "GetElem"}: {llssa.SIMDExtractLane, simdExtract, false},
	{"numeric", "SetElem"}: {llssa.SIMDInsertLane, simdInsert, false},
}

// Resolve only declared official operations, never synthetic wrapper names.
// Direct calls and calls inside the existing SSA wrappers use this same path.
func lookupSIMD(fn *ssa.Function, arch string) (simdOperation, bool) {
	switch arch {
	case "amd64", "arm64", "wasm":
	default:
		return simdOperation{}, false
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok || obj.Pkg() == nil || obj.Pkg().Path() != "simd/archsimd" {
		return simdOperation{}, false
	}
	sig := fn.Signature
	if !types.Identical(sig, obj.Type()) {
		return simdOperation{}, false
	}
	key := simdKey{name: obj.Name()}
	var vector types.Type
	if recv := sig.Recv(); recv != nil {
		vector = recv.Type()
		if _, ok := llssa.SIMDNumericShape(vector); !ok {
			return simdOperation{}, false
		}
		key.receiver = "numeric"
	} else if sig.Results().Len() == 1 {
		vector = sig.Results().At(0).Type()
	}
	desc, ok := simdOperations[key]
	if !ok || !desc.matches(sig, vector) {
		return simdOperation{}, false
	}
	return desc, true
}

func (d simdOperation) matches(sig *types.Signature, vector types.Type) bool {
	if vector == nil || sig.Variadic() || sig.Results().Len() != 1 {
		return false
	}
	lanes, ok := llssa.SIMDNumericShape(vector)
	if !ok {
		return false
	}
	if d.integerOnly && lanes.Elem().Underlying().(*types.Basic).Info()&types.IsInteger == 0 {
		return false
	}
	var params []types.Type
	result := vector
	switch d.signature {
	case simdBinary:
		params = []types.Type{vector}
	case simdExtract:
		params, result = []types.Type{types.Typ[types.Uint8]}, lanes.Elem()
	case simdInsert:
		params = []types.Type{types.Typ[types.Uint8], lanes.Elem()}
	default:
		return false
	}
	if sig.Recv() == nil {
		params = append([]types.Type{vector}, params...)
	}
	if sig.Params().Len() != len(params) || !types.Identical(sig.Results().At(0).Type(), result) {
		return false
	}
	for i, typ := range params {
		if !types.Identical(sig.Params().At(i).Type(), typ) {
			return false
		}
	}
	return true
}

func (p *context) simdOperation(fn *ssa.Function) (simdOperation, bool) {
	return lookupSIMD(fn, p.prog.Target().GOARCH)
}

func (p *context) simdCall(b llssa.Builder, fn *ssa.Function, args []ssa.Value) llssa.Expr {
	desc, ok := p.simdOperation(fn)
	if !ok {
		panic("invalid SIMD intrinsic")
	}
	return b.SIMD(desc.op, p.compileValues(b, args, fnNormal)...)
}
