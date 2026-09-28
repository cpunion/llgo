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

// Computation uses LLVM vectors; storage and call carriers remain Go aggregates.
func (b Builder) simd128Vector(x Expr) llvm.Value {
	lanes := simdLanes(x.RawType())
	elem := b.Prog.toType(lanes.Elem())
	vec := llvm.Undef(llvm.VectorType(elem.ll, int(lanes.Len())))
	values := b.impl.CreateExtractValue(x.impl, 1, "")
	for i := 0; i < int(lanes.Len()); i++ {
		lane := b.impl.CreateExtractValue(values, i, "")
		vec = b.impl.CreateInsertElement(vec, lane, llvm.ConstInt(b.Prog.tyInt32(), uint64(i), false), "")
	}
	return vec
}

func (b Builder) simd128Storage(vec llvm.Value, typ Type) Expr {
	lanes := simdLanes(typ.RawType())
	array := llvm.Undef(b.Prog.toType(lanes).ll)
	for i := 0; i < int(lanes.Len()); i++ {
		lane := b.impl.CreateExtractElement(vec, llvm.ConstInt(b.Prog.tyInt32(), uint64(i), false), "")
		array = b.impl.CreateInsertValue(array, lane, i, "")
	}
	return Expr{b.impl.CreateInsertValue(llvm.ConstNull(typ.ll), array, 1, ""), typ}
}

// simdBinary uses the actual element type for integer and floating arithmetic.
func (b Builder) simdBinary(op SIMDOp, x, y Expr) Expr {
	a, c := b.simd128Vector(x), b.simd128Vector(y)
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
	return b.simd128Storage(v, x.Type)
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
	v := b.impl.CreateExtractElement(b.simd128Vector(x), i, "")
	elem := simdLanes(x.RawType()).Elem()
	return Expr{v, b.Prog.toType(elem)}
}

func (b Builder) simdSetElem(x, index, value Expr) Expr {
	i := b.simd128Index(x, index)
	v := b.impl.CreateInsertElement(b.simd128Vector(x), value.impl, i, "")
	return b.simd128Storage(v, x.Type)
}
