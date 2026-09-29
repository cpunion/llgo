package ssa

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/xgo-dev/llvm"
)

// SIMDOp identifies semantic operations independently of Go syntax tokens.
type SIMDOp uint8

const (
	SIMDAdd SIMDOp = iota
	SIMDSub
	SIMDAnd
	SIMDOr
	SIMDXor
	SIMDExtractLane
	SIMDInsertLane
)

// SIMDNumericShape validates the official numeric aggregate representation.
// Keep storage knowledge here, separate from operation selection and features.
func SIMDNumericShape(typ types.Type) (*types.Array, bool) {
	named, ok := types.Unalias(typ).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "simd/archsimd" {
		return nil, false
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok || st.NumFields() != 2 {
		return nil, false
	}
	lanes, ok := st.Field(1).Type().(*types.Array)
	if !ok {
		return nil, false
	}
	elem, ok := lanes.Elem().(*types.Basic)
	if !ok {
		return nil, false
	}
	var bits int64
	switch elem.Kind() {
	case types.Int8, types.Uint8:
		bits = 8
	case types.Int16, types.Uint16:
		bits = 16
	case types.Int32, types.Uint32, types.Float32:
		bits = 32
	case types.Int64, types.Uint64, types.Float64:
		bits = 64
	default:
		return nil, false
	}
	name := elem.Name()
	expected := fmt.Sprintf("%s%sx%d", strings.ToUpper(name[:1]), name[1:], lanes.Len())
	if named.Obj().Name() != expected || lanes.Len()*bits != 128 {
		return nil, false
	}
	tag, ok := st.Field(0).Type().(*types.Named)
	if !ok || tag.Obj().Pkg() != named.Obj().Pkg() || tag.Obj().Name() != "v128" {
		return nil, false
	}
	if (&types.StdSizes{WordSize: 8, MaxAlign: 8}).Sizeof(tag) != 0 {
		return nil, false
	}
	return lanes, true
}

func simdLanes(typ types.Type) *types.Array {
	lanes, ok := SIMDNumericShape(typ)
	if !ok {
		panic("unsupported SIMD numeric storage: " + typ.String())
	}
	return lanes
}

// SIMD applies the selected operation's feature requirements before lowering.
// These seven implementations need only baseline native instructions; wasm
// requires SIMD128. Future feature-specific implementations extend this entry.
func (b Builder) SIMD(op SIMDOp, args ...Expr) Expr {
	b.simdFeatures(op)
	switch op {
	case SIMDExtractLane:
		return b.simdGetElem(args[0], args[1])
	case SIMDInsertLane:
		return b.simdSetElem(args[0], args[1], args[2])
	default:
		return b.simdBinary(op, args[0], args[1])
	}
}

func (b Builder) simdFeatures(op SIMDOp) {
	switch op {
	case SIMDAdd, SIMDSub, SIMDAnd, SIMDOr, SIMDXor, SIMDExtractLane, SIMDInsertLane:
	default:
		panic("unsupported SIMD operation")
	}
	b.requireSIMDFeatures()
}

func (b Builder) requireSIMDFeatures() {
	if b.Prog.Target().GOARCH == "wasm" {
		features := "+simd128"
		for _, attr := range b.Func.impl.GetFunctionAttributes() {
			if attr.IsString() && attr.GetStringKind() == "target-features" {
				features = attr.GetStringValue()
				if !strings.Contains(","+features+",", ",+simd128,") {
					features += ",+simd128"
				}
			}
		}
		b.Func.impl.AddFunctionAttr(b.Prog.ctx.CreateStringAttribute("target-features", features))
	}
}

// SIMD values stay vectors. Aggregate conversion is restricted to Go storage
// and source-level access to the underlying fields inside archsimd.
func (b Builder) simdFromStorage(value llvm.Value, typ Type) llvm.Value {
	lanes := simdLanes(typ.RawType())
	array := b.impl.CreateExtractValue(value, 1, "")
	vec := llvm.Undef(typ.ll)
	for i := 0; i < int(lanes.Len()); i++ {
		lane := b.impl.CreateExtractValue(array, i, "")
		vec = b.impl.CreateInsertElement(vec, lane, llvm.ConstInt(b.Prog.tyInt32(), uint64(i), false), "")
	}
	return vec
}

func (b Builder) simdToStorage(value llvm.Value, typ Type) llvm.Value {
	storage := b.Prog.storageType(typ)
	lanes := simdLanes(typ.RawType())
	array := llvm.Undef(b.Prog.rawType(lanes).ll)
	for i := 0; i < int(lanes.Len()); i++ {
		lane := b.impl.CreateExtractElement(value, llvm.ConstInt(b.Prog.tyInt32(), uint64(i), false), "")
		array = b.impl.CreateInsertValue(array, lane, i, "")
	}
	return b.impl.CreateInsertValue(llvm.ConstNull(storage), array, 1, "")
}

// simdBinary uses the actual element type for integer and floating arithmetic.
func (b Builder) simdBinary(op SIMDOp, x, y Expr) Expr {
	a, c := x.impl, y.impl
	floating := simdLanes(x.RawType()).Elem().Underlying().(*types.Basic).Info()&types.IsFloat != 0
	var v llvm.Value
	switch op {
	case SIMDAdd:
		if floating {
			v = b.impl.CreateFAdd(a, c, "")
		} else {
			v = b.impl.CreateAdd(a, c, "")
		}
	case SIMDSub:
		if floating {
			v = b.impl.CreateFSub(a, c, "")
		} else {
			v = b.impl.CreateSub(a, c, "")
		}
	case SIMDAnd:
		v = b.impl.CreateAnd(a, c, "")
	case SIMDOr:
		v = b.impl.CreateOr(a, c, "")
	case SIMDXor:
		v = b.impl.CreateXor(a, c, "")
	default:
		panic("invalid SIMD128 binary operation")
	}
	return Expr{v, x.Type}
}

func (b Builder) simd128Index(x, index Expr) llvm.Value {
	lanes := simdLanes(x.RawType()).Len()
	// Check even when ordinary slice bounds checks are disabled: an invalid
	// immediate is an intrinsic error and must never become LLVM poison.
	bad := Expr{llvm.CreateICmp(b.impl, llvm.IntUGE, index.impl, llvm.ConstInt(index.ll, uint64(lanes), false)), b.Prog.Bool()}
	blocks := b.Func.MakeBlocks(2)
	b.If(bad, blocks[0], blocks[1])
	b.SetBlockEx(blocks[0], AtEnd, false)
	b.Call(b.Pkg.rtFunc("PanicSIMDImmediate"))
	b.Unreachable()
	b.SetBlockEx(blocks[1], AtEnd, false)
	b.blk.last = blocks[1].last
	return b.impl.CreateZExt(index.impl, b.Prog.tyInt32(), "")
}

// simdGetElem and simdSetElem also support dynamic uint8 indices.
func (b Builder) simdGetElem(x, index Expr) Expr {
	i := b.simd128Index(x, index)
	v := b.impl.CreateExtractElement(x.impl, i, "")
	elem := simdLanes(x.RawType()).Elem()
	return Expr{v, b.Prog.toType(elem)}
}

func (b Builder) simdSetElem(x, index, value Expr) Expr {
	i := b.simd128Index(x, index)
	v := b.impl.CreateInsertElement(x.impl, value.impl, i, "")
	return Expr{v, x.Type}
}

// A vector ABI also needs SIMD when a function only forwards values, without
// invoking an intrinsic. Inspect the completed IR so wrappers and reflection
// bridges receive the same feature requirement as arithmetic functions.
func (b Builder) finishSIMDFeatures() {
	if b.Prog.Target().GOARCH != "wasm" {
		return
	}
	if llvmTypeHasVector(b.Func.ll) {
		b.requireSIMDFeatures()
		return
	}
	for block := b.Func.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
			if llvmTypeHasVector(inst.Type()) {
				b.requireSIMDFeatures()
				return
			}
			for i := 0; i < inst.OperandsCount(); i++ {
				if llvmTypeHasVector(inst.Operand(i).Type()) {
					b.requireSIMDFeatures()
					return
				}
			}
		}
	}
}

func llvmTypeHasVector(t llvm.Type) bool {
	switch t.TypeKind() {
	case llvm.VectorTypeKind:
		return true
	case llvm.ArrayTypeKind:
		return llvmTypeHasVector(t.ElementType())
	case llvm.StructTypeKind:
		for _, elem := range t.StructElementTypes() {
			if llvmTypeHasVector(elem) {
				return true
			}
		}
	case llvm.FunctionTypeKind:
		if llvmTypeHasVector(t.ReturnType()) {
			return true
		}
		for _, param := range t.ParamTypes() {
			if llvmTypeHasVector(param) {
				return true
			}
		}
	}
	return false
}

// Native libffi classifies source structs rather than LLVM vectors. Reuse the
// typed reflection bridge for signatures whose native ABI contains SIMD values.
func (p Program) simdReflectSignature(sig *types.Signature) bool {
	arch := p.Target().effectiveGOARCH()
	if arch != "amd64" && arch != "arm64" {
		return false
	}
	if recv := sig.Recv(); recv != nil {
		if _, ok := SIMDNumericShape(recv.Type()); ok {
			return true
		}
	}
	for _, tuple := range []*types.Tuple{sig.Params(), sig.Results()} {
		for i := 0; i < tuple.Len(); i++ {
			if _, ok := SIMDNumericShape(tuple.At(i).Type()); ok {
				return true
			}
		}
	}
	return false
}
