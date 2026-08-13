/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ssa

import (
	"fmt"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// -----------------------------------------------------------------------------

// The FieldAddr instruction yields the address of Field of *struct X.
//
// The field is identified by its index within the field list of the
// struct type of X.
//
// Dynamically, this instruction panics if X evaluates to a nil
// pointer.
//
// Type() returns a (possibly named) *types.Pointer.
//
// Example printed form:
//
//	t1 = &t0.name [#1]
func (b Builder) FieldAddr(x Expr, idx int) Expr {
	return b.fieldAddr(x, idx, false)
}

// FieldAddrKnownNonNil yields the address of a field without synthesizing the
// low-level static-null fallback. The caller must already own the
// source-language nil edge, as structured coroutine FieldAddr lowering does.
func (b Builder) FieldAddrKnownNonNil(x Expr, idx int) Expr {
	return b.fieldAddr(x, idx, true)
}

func (b Builder) fieldAddr(x Expr, idx int, knownNonNil bool) Expr {
	dbgInstrf("FieldAddr %v, %d\n", x.impl, idx)
	prog := b.Prog
	if !knownNonNil {
		b.assertStaticNilDeref(x)
	}
	tstruc := prog.Elem(x.Type)
	telem := prog.Field(tstruc, idx)
	pt := prog.Pointer(telem)
	return Expr{llvm.CreateStructGEP(b.impl, tstruc.ll, x.impl, idx), pt}
}

// The Field instruction yields the value of Field of struct X.
func (b Builder) Field(x Expr, idx int) Expr {
	dbgInstrf("Field %v, %d\n", x.impl, idx)
	return b.getField(x, idx)
}

func (b Builder) getField(x Expr, idx int) Expr {
	tfld := b.Prog.Field(x.Type, idx)
	fld := llvm.CreateExtractValue(b.impl, x.impl, idx)
	return Expr{fld, tfld}
}

// -----------------------------------------------------------------------------

func (b Builder) Complex(r, i Expr) Expr {
	dbgInstrf("Complex %v, %v\n", r.impl, i.impl)
	prog := b.Prog
	var t Type
	switch kind := r.raw.Type.Underlying().(*types.Basic).Kind(); kind {
	case types.Float64:
		t = prog.Complex128()
	case types.Float32:
		t = prog.Complex64()
	}
	return b.aggregateValue(t, r.impl, i.impl)
}

// MakeString creates a new string from a C string pointer and length.
func (b Builder) MakeString(cstr Expr, n ...Expr) (ret Expr) {
	dbgInstrf("MakeString %v\n", cstr.impl)
	pkg := b.Pkg
	prog := b.Prog
	ret.Type = prog.String()
	if len(n) == 0 {
		ret.impl = b.Call(pkg.rtFunc("StringFromCStr"), cstr).impl
	} else {
		// TODO(xsw): remove Convert
		ret.impl = b.Call(pkg.rtFunc("StringFrom"), cstr, b.Convert(prog.Int(), n[0])).impl
	}
	return
}

// StringData returns the data pointer of a string.
func (b Builder) StringData(x Expr) Expr {
	dbgInstrf("StringData %v\n", x.impl)
	ptr := llvm.CreateExtractValue(b.impl, x.impl, 0)
	return Expr{ptr, b.Prog.Pointer(b.Prog.Byte())}
}

// StringLen returns the length of a string.
func (b Builder) StringLen(x Expr) Expr {
	dbgInstrf("StringLen %v\n", x.impl)
	ptr := llvm.CreateExtractValue(b.impl, x.impl, 1)
	return Expr{ptr, b.Prog.Int()}
}

// -----------------------------------------------------------------------------

// SliceData returns the data pointer of a slice.
func (b Builder) SliceData(x Expr) Expr {
	dbgInstrf("SliceData %v\n", x.impl)
	ptr := llvm.CreateExtractValue(b.impl, x.impl, 0)
	ty := x.Type.RawType()
	tySlice := ty.Underlying().(*types.Slice)
	return Expr{ptr, b.Prog.Pointer(b.Prog.rawType(tySlice.Elem()))}
}

// SliceLen returns the length of a slice.
func (b Builder) SliceLen(x Expr) Expr {
	dbgInstrf("SliceLen %v\n", x.impl)
	ptr := llvm.CreateExtractValue(b.impl, x.impl, 1)
	return Expr{ptr, b.Prog.Int()}
}

// SliceCap returns the length of a slice cap.
func (b Builder) SliceCap(x Expr) Expr {
	dbgInstrf("SliceCap %v\n", x.impl)
	ptr := llvm.CreateExtractValue(b.impl, x.impl, 2)
	return Expr{ptr, b.Prog.Int()}
}

// -----------------------------------------------------------------------------

// The IndexAddr instruction yields the address of the element at
// index `idx` of collection `x`.  `idx` is an integer expression.
//
// The elements of maps and strings are not addressable; use Lookup (map),
// Index (string), or MapUpdate instead.
//
// Dynamically, this instruction panics if `x` evaluates to a nil *array
// pointer.
//
// Example printed form:
//
//	t2 = &t0[t1]
func (b Builder) IndexAddr(x, idx Expr) Expr {
	dbgInstrf("IndexAddr %v, %v\n", x.impl, idx.impl)
	prog := b.Prog
	telem := prog.Index(x.Type)
	ptr := x
	switch t := x.raw.Type.Underlying().(type) {
	case *types.Slice:
		// Keep the ordinary lowering order stable: materialize the slice data
		// word before its length and bounds predicate. Structured coroutine
		// lowering calls IndexAddrUnchecked only after its explicit fault edge.
		ptr = b.SliceData(x)
		max := b.SliceLen(x)
		idx = b.checkIndex(idx, max)
	case *types.Pointer:
		ar := t.Elem().Underlying().(*types.Array)
		max := prog.IntVal(uint64(ar.Len()), prog.Int())
		idx = b.checkIndex(idx, max)
		if !isKnownNonNilArrayBase(x.impl) {
			b.AssertNilDeref(x)
		}
	}
	return b.indexAddrUnchecked(telem, ptr, idx)
}

// IndexAddrUnchecked emits only the element address calculation. The caller
// must already have established Go's bounds rule and, for *array, the non-nil
// base rule. Structured coroutine lowering uses this after routing a failed
// check through its explicit-status outcome instead of a native-stack panic.
func (b Builder) IndexAddrUnchecked(x, idx Expr) Expr {
	prog := b.Prog
	telem := prog.Index(x.Type)
	ptr := x
	if _, slice := x.raw.Type.Underlying().(*types.Slice); slice {
		ptr = b.SliceData(x)
	}
	return b.indexAddrUnchecked(telem, ptr, idx)
}

func (b Builder) indexAddrUnchecked(telem Type, ptr, idx Expr) Expr {
	pt := b.Prog.Pointer(telem)
	idx = b.normalizeIndex(idx)
	indices := []llvm.Value{idx.impl}
	return Expr{llvm.CreateInBoundsGEP(b.impl, telem.ll, ptr.impl, indices), pt}
}

func isKnownNonNilArrayBase(v llvm.Value) bool {
	if !v.IsAGlobalValue().IsNil() || !v.IsAAllocaInst().IsNil() {
		return true
	}
	if call := v.IsACallInst(); !call.IsNil() {
		if fn := call.CalledValue().IsAFunction(); !fn.IsNil() {
			switch fn.Name() {
			case "github.com/goplus/llgo/runtime/internal/runtime.AllocU",
				"github.com/goplus/llgo/runtime/internal/runtime.AllocZ":
				return true
			}
		}
	}
	return false
}

func isConstantInt(x Expr) (v int64, ok bool) {
	if rv := x.impl.IsAConstantInt(); !rv.IsNil() {
		v = rv.SExtValue()
		ok = true
	}
	return
}

func isConstantUint(x Expr) (v uint64, ok bool) {
	if rv := x.impl.IsAConstantInt(); !rv.IsNil() {
		v = rv.ZExtValue()
		ok = true
	}
	return
}

func checkRange(idx Expr, max Expr) (checkMin, checkMax bool) {
	if idx.kind == vkSigned {
		if v, ok := isConstantInt(idx); ok {
			if v < 0 {
				checkMin = true
			}
			if m, ok := isConstantInt(max); ok {
				if v >= m {
					checkMax = true
				}
			} else {
				checkMax = true
			}
		} else {
			checkMin = true
			checkMax = true
		}
	} else {
		if v, ok := isConstantUint(idx); ok {
			if m, ok := isConstantUint(max); ok {
				if v >= m {
					checkMax = true
				}
			} else {
				checkMax = true
			}
		} else {
			checkMax = true
		}
	}
	return
}

func (b Builder) boundsArg(idx Expr) (Expr, bool) {
	signed := idx.kind == vkSigned
	typ := b.Prog.Int64()
	if idx.impl.Type() != typ.ll {
		idx.impl = castInt(b, idx.impl, idx.Type, typ)
	}
	idx.Type = typ
	return idx, signed
}

// BoundsOperand widens an integer bound to the exact 64-bit bit pattern used
// by runtime boundsError and reports whether that pattern must be interpreted
// as signed. Unlike FitIntSize, it does not truncate a uint64 index on 32-bit
// targets.
func (b Builder) BoundsOperand(idx Expr) (Expr, bool) {
	return b.boundsArg(idx)
}

// check index >= 0 && index < max and size to uint
func (b Builder) checkIndex(idx Expr, max Expr) Expr {
	idx, check := b.IndexBounds(idx, max)
	if !check.IsNil() {
		boundsIdx, signed := b.boundsArg(idx)
		b.InlineCall(b.Pkg.rtFunc("CheckIndexRange"), check, boundsIdx, b.Prog.BoolVal(signed), max)
	}
	return idx
}

// IndexBounds returns a target-width index and the exact predicate that is
// true when the Go index operation is out of range. It emits no panic helper.
func (b Builder) IndexBounds(idx Expr, max Expr) (Expr, Expr) {
	prog := b.Prog
	var checkMin, checkMax bool
	if !prog.disableBoundsChecks {
		checkMin, checkMax = checkRange(idx, max)
	}
	signed := idx.kind == vkSigned
	var typ Type
	if signed {
		typ = prog.Int()
	} else {
		typ = prog.Uint()
	}
	if prog.SizeOf(idx.Type) != prog.SizeOf(typ) {
		srcType := idx.Type
		idx.Type = typ
		idx.impl = castUintptr(b, idx.impl, srcType, typ)
	}
	if prog.disableBoundsChecks {
		return idx, Expr{}
	}
	// check range expr
	var check Expr
	if checkMin {
		zero := llvm.ConstInt(idx.ll, 0, false)
		check = Expr{llvm.CreateICmp(b.impl, llvm.IntSLT, idx.impl, zero), prog.Bool()}
	}
	if checkMax {
		// max is a non-negative len/cap value. Unsigned comparison is valid for
		// both signed and unsigned indexes, and signed negatives fail as large
		// unsigned values.
		r := Expr{llvm.CreateICmp(b.impl, llvm.IntUGE, idx.impl, max.impl), prog.Bool()}
		if check.IsNil() {
			check = r
		} else {
			check = Expr{b.impl.CreateOr(r.impl, check.impl, ""), prog.Bool()}
		}
	}
	return idx, check
}

func (b Builder) normalizeIndex(idx Expr) Expr {
	prog := b.Prog
	typ := prog.Uint()
	if idx.kind == vkSigned {
		typ = prog.Int()
	}
	if prog.SizeOf(idx.Type) != prog.SizeOf(typ) {
		srcType := idx.Type
		idx.Type = typ
		idx.impl = castUintptr(b, idx.impl, srcType, typ)
	}
	return idx
}

// The Index instruction yields element Index of collection X, an array,
// string or type parameter containing an array, a string, a pointer to an,
// array or a slice.
//
// Example printed form:
//
//	t2 = t0[t1]
func (b Builder) Index(x, idx Expr, takeAddr func() (addr Expr, zero bool)) Expr {
	dbgInstrf("Index %v, %v\n", x.impl, idx.impl)
	prog := b.Prog
	var telem Type
	var ptr Expr
	var max Expr
	var zero bool
	switch t := x.raw.Type.Underlying().(type) {
	case *types.Basic:
		if t.Kind() != types.String {
			panic(fmt.Errorf("invalid operation: cannot index %v", t))
		}
		telem = prog.rawType(types.Typ[types.Byte])
		ptr = b.StringData(x)
		max = b.StringLen(x)
	case *types.Array:
		telem = prog.Index(x.Type)
		ptr, zero = takeAddr()
		max = prog.IntVal(uint64(t.Len()), prog.Int())
	}
	idx = b.checkIndex(idx, max)
	return b.indexUnchecked(x, idx, telem, ptr, zero)
}

// IndexUnchecked emits an array/string element load without a bounds helper.
// The caller must first branch on IndexBounds.
func (b Builder) IndexUnchecked(x, idx Expr, takeAddr func() (addr Expr, zero bool)) Expr {
	dbgInstrf("IndexUnchecked %v, %v\n", x.impl, idx.impl)
	prog := b.Prog
	var telem Type
	var ptr Expr
	var zero bool
	switch t := x.raw.Type.Underlying().(type) {
	case *types.Basic:
		if t.Kind() != types.String {
			panic(fmt.Errorf("invalid operation: cannot index %v", t))
		}
		telem = prog.rawType(types.Typ[types.Byte])
		ptr = b.StringData(x)
	case *types.Array:
		telem = prog.Index(x.Type)
		ptr, zero = takeAddr()
	default:
		panic(fmt.Errorf("invalid unchecked index base %v", x.raw.Type))
	}
	idx = b.normalizeIndex(idx)
	return b.indexUnchecked(x, idx, telem, ptr, zero)
}

func (b Builder) indexUnchecked(x, idx Expr, telem Type, ptr Expr, zero bool) Expr {
	if zero {
		return b.Prog.Zero(telem)
	}
	if ptr.IsNil() {
		ptr = b.Alloc(x.Type, false)
		b.impl.CreateStore(x.impl, ptr.impl)
	}
	pt := b.Prog.Pointer(telem)
	indices := []llvm.Value{idx.impl}
	buf := Expr{llvm.CreateInBoundsGEP(b.impl, telem.ll, ptr.impl, indices), pt}
	return b.Load(buf)
}

// -----------------------------------------------------------------------------

// The Slice instruction yields a slice of an existing string, slice
// or *array X between optional integer bounds Low and High.
//
// Dynamically, this instruction panics if X evaluates to a nil *array
// pointer.
//
// Type() returns string if the type of X was string, otherwise a
// *types.Slice with the same element type as X.
//
// Example printed form:
//
//	t1 = slice t0[1:]
func (b Builder) Slice(x, low, high, max Expr) (ret Expr) {
	dbgInstrf("Slice %v, %v, %v\n", x.impl, low.impl, high.impl)
	prog := b.Prog
	var nCap Expr
	var nEltSize Expr
	var base Expr
	var lowIsNil = low.IsNil()
	var lowArg Expr
	var highArg Expr
	var maxArg Expr
	var lowSigned = true
	var highSigned = true
	var maxSigned = true
	var upperIsLen bool
	if lowIsNil {
		low = prog.IntVal(0, prog.Int())
		lowArg = prog.IntVal(0, prog.Int64())
	} else {
		lowArg, lowSigned = b.boundsArg(low)
		low = b.FitIntSize(low)
	}
	if !high.IsNil() {
		highArg, highSigned = b.boundsArg(high)
		high = b.FitIntSize(high)
	}
	if !max.IsNil() {
		maxArg, maxSigned = b.boundsArg(max)
		max = b.FitIntSize(max)
	}
	switch t := x.raw.Type.Underlying().(type) {
	case *types.Basic:
		if t.Kind() != types.String {
			panic(fmt.Errorf("invalid operation: cannot slice %v", t))
		}
		if high.IsNil() {
			high = b.StringLen(x)
			highArg, highSigned = b.boundsArg(high)
		}
		ret.Type = x.Type
		if prog.disableBoundsChecks {
			ret.impl = b.stringSliceUnchecked(x, low, high).impl
		} else {
			ret.impl = b.InlineCall(b.Pkg.rtFunc("StringSlice2"), x, lowArg, highArg, prog.BoolVal(lowSigned), prog.BoolVal(highSigned)).impl
		}
		return
	case *types.Slice:
		nEltSize = SizeOf(prog, prog.Index(x.Type))
		nCap = b.SliceCap(x)
		if high.IsNil() {
			high = b.SliceLen(x)
			highArg, highSigned = b.boundsArg(high)
		}
		ret.Type = x.Type
		base = b.SliceData(x)
	case *types.Pointer:
		telem := t.Elem()
		switch te := telem.Underlying().(type) {
		case *types.Array:
			elem := prog.rawType(te.Elem())
			ret.Type = prog.Slice(elem)
			nEltSize = SizeOf(prog, elem)
			nCap = prog.IntVal(uint64(te.Len()), prog.Int())
			upperIsLen = true
			if high.IsNil() {
				if lowIsNil && max.IsNil() && !prog.disableBoundsChecks {
					ret.impl = b.unsafeSlice(x, nCap.impl, nCap.impl).impl
					return
				}
				high = nCap
				highArg, highSigned = b.boundsArg(high)
			}
			base = x
		}
	}
	if prog.disableBoundsChecks {
		if _, ok := x.raw.Type.Underlying().(*types.Pointer); ok && !isKnownNonNilArrayBase(x.impl) {
			b.AssertNilDeref(x)
		}
		upper := nCap
		if !max.IsNil() {
			upper = max
		}
		ret.impl = b.sliceUnchecked(ret.Type, base, low, high, upper).impl
		return
	}
	if max.IsNil() {
		ret.impl = b.InlineCall(
			b.Pkg.rtFunc("NewSlice2"),
			base,
			nEltSize,
			nCap,
			lowArg,
			highArg,
			prog.BoolVal(lowSigned),
			prog.BoolVal(highSigned),
			prog.BoolVal(upperIsLen),
		).impl
		return
	}
	ret.impl = b.InlineCall(
		b.Pkg.rtFunc("NewSlice3Bounds"),
		base,
		nEltSize,
		nCap,
		lowArg,
		highArg,
		maxArg,
		prog.BoolVal(lowSigned),
		prog.BoolVal(highSigned),
		prog.BoolVal(maxSigned),
		prog.BoolVal(upperIsLen),
	).impl
	return
}

// SliceBounds returns target-width low/high/max values and the exact predicate
// that is true when a two- or three-index Go slice expression is out of range.
// The caller supplies the already selected upper limit: len for strings and
// pointer-to-array values, cap for slices. A nil max selects the two-index
// rules. This emits no panic helper.
func (b Builder) SliceBounds(low, high, max, limit Expr) (nlow, nhigh, nmax, outOfRange Expr) {
	nlow, nhigh, nmax, checks := b.SliceBoundsChecks(low, high, max, limit)
	for _, check := range checks {
		outOfRange = b.orBool(outOfRange, check.OutOfRange)
	}
	return
}

// SliceBoundsCheck is one ordered Go slice-bounds failure. X retains the
// original operand as a 64-bit bit pattern and Signed selects its
// interpretation. Y is target-width because every preceding check proves that
// relational upper operands fit in a non-negative Go int before a later check
// can expose them as boundsError.y.
type SliceBoundsCheck struct {
	OutOfRange Expr
	X          Expr
	Y          Expr
	Signed     bool
}

// SliceBoundsChecks returns normalized target-width low/high/max values and
// the ordered failure predicates required by Go panic semantics:
//
//   - two-index: high > len/cap, then low > high
//   - three-index: max > len/cap, high > max, then low > high
//
// Keeping these predicates separate lets coroutine lowering preserve the
// precise boundsError code and operand that failed instead of collapsing the
// checks into one generic "index out of range" edge.
func (b Builder) SliceBoundsChecks(
	low, high, max, limit Expr,
) (nlow, nhigh, nmax Expr, checks []SliceBoundsCheck) {
	if low.IsNil() || high.IsNil() || limit.IsNil() {
		panic("SliceBoundsChecks requires explicit low, high, and limit values")
	}

	low64, lowSigned := b.boundsArg(low)
	high64, highSigned := b.boundsArg(high)
	limit64, _ := b.boundsArg(limit)
	nlow = b.FitIntSize(low64)
	nhigh = b.FitIntSize(high64)
	nlimit := b.FitIntSize(limit64)
	if max.IsNil() {
		if b.Prog.disableBoundsChecks {
			return
		}
		checks = []SliceBoundsCheck{
			{
				OutOfRange: b.sliceBoundAbove(high64, highSigned, limit64),
				X:          high64,
				Y:          nlimit,
				Signed:     highSigned,
			},
			{
				OutOfRange: b.sliceBoundAbove(low64, lowSigned, high64),
				X:          low64,
				Y:          nhigh,
				Signed:     lowSigned,
			},
		}
	} else {
		max64, maxSigned := b.boundsArg(max)
		nmax = b.FitIntSize(max64)
		if b.Prog.disableBoundsChecks {
			return
		}
		checks = []SliceBoundsCheck{
			{
				OutOfRange: b.sliceBoundAbove(max64, maxSigned, limit64),
				X:          max64,
				Y:          nlimit,
				Signed:     maxSigned,
			},
			{
				OutOfRange: b.sliceBoundAbove(high64, highSigned, max64),
				X:          high64,
				Y:          nmax,
				Signed:     highSigned,
			},
			{
				OutOfRange: b.sliceBoundAbove(low64, lowSigned, high64),
				X:          low64,
				Y:          nhigh,
				Signed:     lowSigned,
			},
		}
	}
	return
}

// sliceBoundAbove implements the inclusive slice-bound relation
//
//	value < 0 || value > upper
//
// while preserving the signedness and width of the original Go integer until
// after the check. upper is an already widened non-negative len/cap or a bound
// whose own validity is checked by the caller.
func (b Builder) sliceBoundAbove(value Expr, signed bool, upper Expr) Expr {
	var out Expr
	if signed {
		zero := llvm.ConstInt(value.ll, 0, false)
		out = Expr{llvm.CreateICmp(b.impl, llvm.IntSLT, value.impl, zero), b.Prog.Bool()}
	}
	above := Expr{llvm.CreateICmp(b.impl, llvm.IntUGT, value.impl, upper.impl), b.Prog.Bool()}
	return b.orBool(out, above)
}

func (b Builder) orBool(first, second Expr) Expr {
	if first.IsNil() {
		return second
	}
	if second.IsNil() {
		return first
	}
	return Expr{b.impl.CreateOr(first.impl, second.impl, ""), b.Prog.Bool()}
}

// SliceUnchecked constructs the result of a validated Go slice expression.
// low/high/max must be target-width values returned by SliceBounds; max is nil
// for a two-index expression. No runtime helper or bounds branch is emitted.
func (b Builder) SliceUnchecked(x, low, high, max Expr) (ret Expr) {
	if x.IsNil() || low.IsNil() || high.IsNil() {
		panic("SliceUnchecked requires an exact base, low, and high")
	}
	length := b.BinOp(token.SUB, high, low)
	switch typ := x.raw.Type.Underlying().(type) {
	case *types.Basic:
		if typ.Kind() != types.String || !max.IsNil() {
			panic("SliceUnchecked basic base must be a two-index string")
		}
		base := b.StringData(x)
		advanced := b.sliceDataAt(base, b.Prog.Byte(), low)
		hasSuffix := b.BinOp(token.LSS, low, b.StringLen(x))
		data := b.SelectValue(hasSuffix, advanced, base)
		ret = b.unsafeString(data.impl, length.impl)
		ret.Type = x.Type
		return
	case *types.Slice:
		capacityEnd := max
		if capacityEnd.IsNil() {
			capacityEnd = b.SliceCap(x)
		}
		capacity := b.BinOp(token.SUB, capacityEnd, low)
		base := b.SliceData(x)
		elem := b.Prog.rawType(typ.Elem())
		advanced := b.sliceDataAt(base, elem, low)
		nonemptyCapacity := b.BinOp(token.NEQ, capacity, b.Prog.IntVal(0, b.Prog.Int()))
		data := b.SelectValue(nonemptyCapacity, advanced, base)
		ret = b.unsafeSlice(data, length.impl, capacity.impl)
		ret.Type = x.Type
		return
	case *types.Pointer:
		array, ok := typ.Elem().Underlying().(*types.Array)
		if !ok {
			panic("SliceUnchecked pointer base is not an array")
		}
		capacityEnd := max
		if capacityEnd.IsNil() {
			capacityEnd = b.Prog.IntVal(uint64(array.Len()), b.Prog.Int())
		}
		capacity := b.BinOp(token.SUB, capacityEnd, low)
		elem := b.Prog.rawType(array.Elem())
		advanced := b.sliceDataAt(x, elem, low)
		nonemptyCapacity := b.BinOp(token.NEQ, capacity, b.Prog.IntVal(0, b.Prog.Int()))
		data := b.SelectValue(nonemptyCapacity, advanced, x)
		ret = b.unsafeSlice(data, length.impl, capacity.impl)
		return
	default:
		panic(fmt.Sprintf("SliceUnchecked has unsupported base %T", typ))
	}
}

func (b Builder) sliceDataAt(base Expr, elem Type, index Expr) Expr {
	index = b.normalizeIndex(index)
	ptr := llvm.CreateGEP(b.impl, elem.ll, base.impl, []llvm.Value{index.impl})
	return Expr{ptr, b.Prog.Pointer(elem)}
}

// stringSliceUnchecked and sliceUnchecked are the ordinary lowering helpers
// used by -B. Structured coroutine lowering uses SliceUnchecked after its
// frozen plan has either emitted or deliberately disabled the same checks.
func (b Builder) stringSliceUnchecked(x, low, high Expr) Expr {
	data := b.StringData(x)
	advanced := b.Advance(data, low)
	beforeEnd := llvm.CreateICmp(b.impl, llvm.IntSLT, low.impl, b.StringLen(x).impl)
	data.impl = llvm.CreateSelect(b.impl, beforeEnd, advanced.impl, data.impl)
	length := b.impl.CreateSub(high.impl, low.impl, "")
	return b.unsafeString(data.impl, length)
}

func (b Builder) sliceUnchecked(t Type, base, low, high, upper Expr) Expr {
	length := b.impl.CreateSub(high.impl, low.impl, "")
	capacity := b.impl.CreateSub(upper.impl, low.impl, "")
	advanced := b.Advance(base, low)
	zero := llvm.ConstInt(capacity.Type(), 0, false)
	hasCapacity := llvm.CreateICmp(b.impl, llvm.IntSGT, capacity, zero)
	base.impl = llvm.CreateSelect(b.impl, hasCapacity, advanced.impl, base.impl)
	ret := b.unsafeSlice(base, length, capacity)
	ret.Type = t
	return ret
}

// SliceLit creates a new slice with the specified elements.
func (b Builder) SliceLit(t Type, elts ...Expr) Expr {
	prog := b.Prog
	telem := prog.Index(t)
	ptr := b.AllocU(telem, int64(len(elts)))
	for i, elt := range elts {
		b.Store(b.Advance(ptr, prog.Val(i)), elt)
	}
	size := llvm.ConstInt(prog.tyInt(), uint64(len(elts)), false)
	return b.unsafeSlice(ptr, size, size)
}

// The MakeSlice instruction yields a slice of length Len backed by a
// newly allocated array of length Cap.
//
// Both Len and Cap must be non-nil Values of integer type.
//
// (Alloc(types.Array) followed by Slice will not suffice because
// Alloc can only create arrays of constant length.)
//
// Type() returns a (possibly named) *types.Slice.
//
// Example printed form:
//
//	t1 = make []string 1:int t0
//	t1 = make StringSlice 1:int t0
func (b Builder) MakeSlice(t Type, len, cap Expr) (ret Expr) {
	dbgInstrf("MakeSlice %v, %v, %v\n", t.RawType(), len.impl, cap.impl)
	prog := b.Prog
	len = b.FitIntSize(len)
	cap = b.FitIntSize(cap)
	telem := prog.Index(t)
	ret = b.InlineCall(b.Pkg.rtFunc("MakeSlice"), len, cap, prog.IntVal(prog.SizeOf(telem), prog.Int()))
	ret.Type = t
	return
}

// fit size to int
func (b Builder) FitIntSize(n Expr) Expr {
	prog := b.Prog
	typ := prog.Int()
	if prog.SizeOf(n.Type) != prog.SizeOf(typ) {
		srcType := n.Type
		n.impl = castInt(b, n.impl, srcType, typ)
	}
	n.Type = typ
	return n
}

// -----------------------------------------------------------------------------

// The MakeMap instruction creates a new hash-table-based map object
// and yields a value of kind map.
//
// t is a (possibly named) *types.Map.
//
// Example printed form:
//
//	t1 = make map[string]int t0
//	t1 = make StringIntMap t0
func (b Builder) MakeMap(t Type, nReserve Expr) (ret Expr) {
	dbgInstrf("MakeMap %v, %v\n", t.RawType(), nReserve.impl)
	if nReserve.IsNil() {
		nReserve = b.Prog.Val(0)
	}
	nReserve = b.FitIntSize(nReserve)
	typ := b.abiType(t.raw.Type)
	ret = b.InlineCall(b.Pkg.rtFunc("MakeMap"), typ, nReserve)
	ret.Type = t
	return
}

// The Lookup instruction yields element Index of collection map X.
// Index is the appropriate key type.
//
// If CommaOk, the result is a 2-tuple of the value above and a
// boolean indicating the result of a map membership test for the key.
// The components of the tuple are accessed using Extract.
//
// Example printed form:
//
//	t2 = t0[t1]
//	t5 = t3[t4],ok
func (b Builder) Lookup(x, key Expr, commaOk bool) (ret Expr) {
	dbgInstrf("Lookup %v, %v, %v\n", x.impl, key.impl, commaOk)
	prog := b.Prog
	typ := b.abiType(x.raw.Type)
	vtyp := prog.Elem(x.Type)
	ptr := b.mapKeyPtr(key)
	if commaOk {
		vals := b.Call(b.Pkg.rtFunc("MapAccess2"), typ, x, ptr)
		// The Go runtime map ABI never returns a nil element pointer: a miss
		// points at its zero-value storage. Do not manufacture a second
		// source-language nil edge after the exact MapAccess2 call.
		val := b.LoadKnownNonNil(Expr{b.impl.CreateExtractValue(vals.impl, 0, ""), prog.Pointer(vtyp)})
		ok := b.impl.CreateExtractValue(vals.impl, 1, "")
		t := prog.Struct(vtyp, prog.Bool())
		return b.aggregateValue(t, val.impl, ok)
	} else {
		val := b.Call(b.Pkg.rtFunc("MapAccess1"), typ, x, ptr)
		val.Type = prog.Pointer(vtyp)
		// MapAccess1 follows the same non-nil zero-value-pointer ABI.
		ret = b.LoadKnownNonNil(val)
	}
	return
}

// The MapUpdate instruction updates the association of Map[Key] to
// Value.
//
// Pos() returns the ast.KeyValueExpr.Colon or ast.IndexExpr.Lbrack,
// if explicit in the source.
//
// Example printed form:
//
//	t0[t1] = t2
func (b Builder) MapUpdate(m, k, v Expr) {
	// Convert function declarations to proper closure form when stored in maps.
	// This ensures function values are correctly wrapped as closures, similar to
	// interface assignment (see MakeInterface).
	if v.kind == vkFuncDecl {
		typ := b.Prog.Type(v.raw.Type, InGo)
		v = checkExpr(v, typ.raw.Type, b)
	}
	dbgInstrf("MapUpdate %v[%v] = %v\n", m.impl, k.impl, v.impl)
	typ := b.abiType(m.raw.Type)
	ptr := b.mapKeyPtr(k)
	ret := b.Call(b.Pkg.rtFunc("MapAssign"), typ, m, ptr)
	ret.Type = b.Prog.Pointer(v.Type)
	b.Store(ret, v)
}

// key => unsafe.Pointer
func (b Builder) mapKeyPtr(x Expr) Expr {
	typ := x.Type
	vtyp := b.Prog.VoidPtr()
	vptr := b.AllocU(typ)
	b.Store(vptr, x)
	return Expr{vptr.impl, vtyp}
}

// -----------------------------------------------------------------------------

// The Range instruction yields an iterator over the domain and range
// of X, which must be a string or map.
//
// Elements are accessed via Next.
//
// Type() returns an opaque and degenerate "rangeIter" type.
//
// Pos() returns the ast.RangeStmt.For.
//
// Example printed form:
//
//	t0 = range "hello":string
func (b Builder) Range(x Expr) Expr {
	switch x.kind {
	case vkString:
		return b.InlineCall(b.Pkg.rtFunc("NewStringIter"), x)
	case vkMap:
		typ := b.abiType(x.raw.Type)
		return b.InlineCall(b.Pkg.rtFunc("NewMapIter"), typ, x)
	}
	panic("unsupport range for " + x.raw.Type.String())
}

// The Next instruction reads and advances the (map or string)
// iterator Iter and returns a 3-tuple value (ok, k, v).  If the
// iterator is not exhausted, ok is true and k and v are the next
// elements of the domain and range, respectively.  Otherwise ok is
// false and k and v are undefined.
//
// Components of the tuple are accessed using Extract.
//
// The IsString field distinguishes iterators over strings from those
// over maps, as the Type() alone is insufficient: consider
// map[int]rune.
//
// Type() returns a *types.Tuple for the triple (ok, k, v).
// The types of k and/or v may be types.Invalid.
//
// Example printed form:
//
//	t1 = next t0
func (b Builder) Next(typ Type, iter Expr, isString bool) Expr {
	if isString {
		return b.InlineCall(b.Pkg.rtFunc("StringIterNext"), iter)
	}
	prog := b.Prog
	ktyp := prog.Type(typ.raw.Type.Underlying().(*types.Map).Key(), InGo)
	vtyp := prog.Type(typ.raw.Type.Underlying().(*types.Map).Elem(), InGo)
	rets := b.InlineCall(b.Pkg.rtFunc("MapIterNext"), iter)
	ok := b.impl.CreateExtractValue(rets.impl, 0, "")
	t := prog.Struct(prog.Bool(), ktyp, vtyp)
	blks := b.Func.MakeBlocks(3)
	b.If(Expr{ok, prog.Bool()}, blks[0], blks[1])
	b.SetBlockEx(blks[2], AtEnd, false)
	phi := b.Phi(t)
	phi.AddIncoming(b, blks[:2], func(i int, blk BasicBlock) Expr {
		b.SetBlockEx(blk, AtEnd, false)
		if i == 0 {
			k := b.impl.CreateExtractValue(rets.impl, 1, "")
			v := b.impl.CreateExtractValue(rets.impl, 2, "")
			valTrue := aggregateValue(b.impl, t.ll, prog.BoolVal(true).impl,
				llvm.CreateLoad(b.impl, ktyp.ll, k),
				llvm.CreateLoad(b.impl, vtyp.ll, v))
			b.Jump(blks[2])
			return Expr{valTrue, t}
		}
		valFalse := aggregateValue(b.impl, t.ll, prog.BoolVal(false).impl,
			llvm.ConstNull(ktyp.ll),
			llvm.ConstNull(vtyp.ll))
		b.Jump(blks[2])
		return Expr{valFalse, t}
	})
	b.SetBlockEx(blks[2], AtEnd, false)
	b.blk.last = blks[2].last
	return phi.Expr
}

// The MakeChan instruction creates a new channel object and yields a
// value of kind chan.
//
// Type() returns a (possibly named) *types.Chan.
//
// Pos() returns the ast.CallExpr.Lparen for the make(chan) that
// created it.
//
// Example printed form:
//
//	t0 = make chan int 0
//	t0 = make IntChan 0
//
//	type MakeChan struct {
//		register
//		Size Value // int; size of buffer; zero => synchronous.
//	}
func (b Builder) MakeChan(t Type, size Expr) (ret Expr) {
	dbgInstrf("MakeChan %v, %v\n", t.RawType(), size.impl)
	prog := b.Prog
	eltSize := prog.IntVal(prog.SizeOf(prog.Elem(t)), prog.Int())
	size = b.FitIntSize(size)
	ret.Type = t
	ret.impl = b.InlineCall(b.Pkg.rtFunc("NewChan"), eltSize, size).impl
	return
}

// The Send instruction sends X on channel Chan.
//
// Pos() returns the ast.SendStmt.Arrow, if explicit in the source.
//
// Example printed form:
//
//	send t0 <- t1
func (b Builder) Send(ch Expr, x Expr) (ret Expr) {
	dbgInstrf("Send %v, %v\n", ch.impl, x.impl)
	prog := b.Prog
	eltSize := prog.IntVal(prog.SizeOf(prog.Elem(ch.Type)), prog.Int())
	sp := b.StackSave()
	ret = b.InlineCall(b.Pkg.rtFunc("ChanSend"), ch, b.toPtr(x), eltSize)
	b.StackRestore(sp)
	return
}

func (b Builder) toPtr(x Expr) Expr {
	typ := x.Type
	vtyp := b.Prog.VoidPtr()
	vptr := b.Alloc(typ, false)
	b.Store(vptr, x)
	return Expr{vptr.impl, vtyp}
}

func (b Builder) Recv(ch Expr, commaOk bool) (ret Expr) {
	dbgInstrf("Recv %v, %v\n", ch.impl, commaOk)
	prog := b.Prog
	eltSize := prog.IntVal(prog.SizeOf(prog.Elem(ch.Type)), prog.Int())
	etyp := prog.Elem(ch.Type)
	sp := b.StackSave()
	ptr := b.Alloc(etyp, false)
	ok := b.InlineCall(b.Pkg.rtFunc("ChanRecv"), ch, ptr, eltSize)
	val := b.Load(ptr)
	b.StackRestore(sp)
	if commaOk {
		t := prog.Struct(etyp, prog.Bool())
		return b.aggregateValue(t, val.impl, ok.impl)
	} else {
		return val
	}
}

// CoroChanTrySend performs only the nonblocking, non-panicking first attempt
// of a compiler-owned stackless channel send. The caller owns elem storage and
// must enter the exact channel park transaction when false is returned.
func (b Builder) CoroChanTrySend(task, ch, elem Expr) Expr {
	prog := b.Prog
	eltSize := prog.IntVal(prog.SizeOf(prog.Elem(ch.Type)), prog.Int())
	return b.InlineCall(b.Pkg.rtFunc("CoroChanTrySend"), task, ch, elem, eltSize)
}

// CoroChanTryRecv performs only the nonblocking first attempt of a
// compiler-owned stackless channel receive. It returns (recvOK, tryOK); the
// caller must enter the exact channel park transaction when tryOK is false.
func (b Builder) CoroChanTryRecv(task, ch, elem Expr) Expr {
	prog := b.Prog
	eltSize := prog.IntVal(prog.SizeOf(prog.Elem(ch.Type)), prog.Int())
	return b.InlineCall(b.Pkg.rtFunc("CoroChanTryRecv"), task, ch, elem, eltSize)
}

// CoroChanTryClose performs one complete non-panicking channel-close
// transaction. Its scalar result distinguishes success, nil channel, and an
// already closed channel; physical coroutine lowering owns the two language
// panic outcomes through its explicit-status ABI.
func (b Builder) CoroChanTryClose(ch Expr) Expr {
	return b.InlineCall(b.Pkg.rtFunc("CoroChanTryClose"), ch)
}

type SelectState struct {
	Chan  Expr // channel to use (for send or receive)
	Value Expr // value to send (for send)
	Send  bool // direction of case (SendOnly or RecvOnly)
}

// CoroSelect is compiler-owned storage for one blocking channel select in a
// physical LLVM coroutine body. payloads and opsStorage retain the exact
// allocation identities, rather than only aggregate values containing their
// addresses: LLVM CoroSplit must see a direct post-suspend use of every backing
// alloca before it can move that storage into the stackless frame.
type CoroSelect struct {
	fn         Function
	states     []*SelectState
	ops        []Expr
	payloads   []Expr
	opsStorage Expr
	candidates Expr
	storage    Expr
}

// NewCoroSelect evaluates and materializes every already-compiled channel
// operand exactly once. The caller may first use CoroChanSelectTry, then pass
// the same plan to CoroChanSelectPark and CoroChanSelectResume.
func (b Builder) NewCoroSelect(states []*SelectState) *CoroSelect {
	return b.newCoroSelect(b, states)
}

// NewCoroSelectInFrame is the blocking-select form. frame must insert static
// allocas in the physical coroutine entry block, while b initializes their
// contents at the select's actual execution point. Keeping allocation and
// initialization separate makes loop reuse correct and prevents an hchan
// waiter from retaining a resume-local physical stack address.
func (b Builder) NewCoroSelectInFrame(frame Builder, states []*SelectState) *CoroSelect {
	if frame == nil || frame.Func != b.Func {
		panic("ssa: coroutine select frame builder belongs to another function")
	}
	return b.newCoroSelect(frame, states)
}

func (b Builder) newCoroSelect(storageBuilder Builder, states []*SelectState) *CoroSelect {
	if b == nil || b.Func == nil || storageBuilder == nil || storageBuilder.Func != b.Func {
		panic("ssa: coroutine select requires an active function builder")
	}
	ops := make([]Expr, len(states))
	payloads := make([]Expr, len(states))
	for index, state := range states {
		if state == nil || state.Chan.IsNil() {
			panic("ssa: coroutine select requires complete channel states")
		}
		var payload Expr
		if state.Send {
			payload = storageBuilder.AllocaT(state.Value.Type)
			b.Store(payload, state.Value)
		} else {
			elem := b.Prog.Elem(state.Chan.Type)
			payload = storageBuilder.AllocaT(elem)
			// A static select instruction may execute repeatedly in a loop. Reset
			// unselected receive results on every logical execution, not merely
			// once when the coroutine frame is created.
			b.Store(payload, b.Prog.Zero(elem))
		}
		payloads[index] = payload
		ops[index] = b.chanOpWithPayload(state, payload)
	}
	opType := b.Prog.rtType("ChanOp")
	opsStorage := storageBuilder.ArrayAlloca(opType, b.Prog.Val(len(states)))
	for index, op := range ops {
		b.Store(b.Advance(opsStorage, b.Prog.Val(index)), op)
	}
	return &CoroSelect{
		fn:         b.Func,
		states:     states,
		ops:        ops,
		payloads:   payloads,
		opsStorage: opsStorage,
		candidates: storageBuilder.ArrayAlloca(b.Prog.rtType("CoroChanSelectCaseV1"), b.Prog.Val(len(states))),
		storage:    storageBuilder.Alloc(b.Prog.rtType("CoroChanSelectV1"), false),
	}
}

func (b Builder) requireCoroSelect(plan *CoroSelect) {
	if b == nil || plan == nil || plan.fn == nil || plan.fn != b.Func || len(plan.states) != len(plan.ops) ||
		len(plan.payloads) != len(plan.ops) || plan.opsStorage.IsNil() ||
		plan.candidates.IsNil() || plan.storage.IsNil() {
		panic("ssa: invalid coroutine select plan")
	}
}

func (b Builder) coroSelectOpsSlice(plan *CoroSelect) Expr {
	b.requireCoroSelect(plan)
	n := llvm.ConstInt(b.Prog.tyInt(), uint64(len(plan.ops)), false)
	return b.unsafeSlice(plan.opsStorage, n, n)
}

// CoroChanSelectTry performs the randomized, nonblocking, non-panicking first
// pass. It returns the runtime tuple (index, recvOK, tryOK, sendClosed).
func (b Builder) CoroChanSelectTry(plan *CoroSelect) Expr {
	b.requireCoroSelect(plan)
	return b.Call(b.Pkg.rtFunc("CoroChanSelectTry"), b.coroSelectOpsSlice(plan))
}

// CoroChanSelectPark installs all physical cases at the compiler's exact
// before-suspend point.
func (b Builder) CoroChanSelectPark(plan *CoroSelect, g, handle, header Expr) {
	b.requireCoroSelect(plan)
	void := b.Prog.VoidPtr()
	b.Call(
		b.Pkg.rtFunc("CoroChanSelectPark"),
		g,
		handle,
		header,
		b.Convert(void, plan.candidates),
		b.Convert(void, plan.storage),
		b.coroSelectOpsSlice(plan),
	)
}

// CoroChanSelectResume consumes the exact runtime decision and returns
// (index, recvOK, typedStatus).
func (b Builder) CoroChanSelectResume(plan *CoroSelect, g Expr) Expr {
	b.requireCoroSelect(plan)
	// Runtime queue nodes retain every send and receive payload address across
	// the physical suspend. Receive payloads are also loaded below, but send
	// payloads otherwise have no direct resumed SSA use; keep all allocation
	// identities live until CoroSplit has placed them in the coroutine frame.
	// RemoveKeepAliveCallsAfterCoroSplit erases these optimizer-only uses.
	b.KeepAlive(plan.payloads...)
	void := b.Prog.VoidPtr()
	return b.Call(
		b.Pkg.rtFunc("CoroChanSelectResume"),
		g,
		b.Convert(void, plan.candidates),
		b.Convert(void, plan.storage),
		b.coroSelectOpsSlice(plan),
	)
}

// CoroChanSelectResult assembles the x/tools SSA tuple from the chosen prefix
// and the receive-value storage shared by the fast and resumed paths.
func (b Builder) CoroChanSelectResult(plan *CoroSelect, chosen, recvOK Expr) Expr {
	b.requireCoroSelect(plan)
	results := []llvm.Value{chosen.impl, recvOK.impl}
	typs := []Type{b.Prog.Int(), b.Prog.Bool()}
	for index, state := range plan.states {
		if state.Send {
			continue
		}
		etyp := b.Prog.Elem(state.Chan.Type)
		typs = append(typs, etyp)
		// Load the original allocation identity directly. Extracting the same
		// address from a pre-suspend ChanOp aggregate is semantically equivalent,
		// but hides the backing alloca from CoroSplit's frame-liveness analysis.
		// The compiler-owned slot cannot be nil, including for zero-sized values.
		value := b.LoadKnownNonNil(plan.payloads[index])
		results = append(results, value.impl)
	}
	return b.aggregateValue(b.Prog.Struct(typs...), results...)
}

// The Select instruction tests whether (or blocks until) one
// of the specified sent or received states is entered.
//
// Let n be the number of States for which Dir==RECV and T_i (0<=i<n)
// be the element type of each such state's Chan.
// Select returns an n+2-tuple
//
//	(index int, recvOk bool, r_0 T_0, ... r_n-1 T_n-1)
//
// The tuple's components, described below, must be accessed via the
// Extract instruction.
//
// If Blocking, select waits until exactly one state holds, i.e. a
// channel becomes ready for the designated operation of sending or
// receiving; select chooses one among the ready states
// pseudorandomly, performs the send or receive operation, and sets
// 'index' to the index of the chosen channel.
//
// If !Blocking, select doesn't block if no states hold; instead it
// returns immediately with index equal to -1.
//
// If the chosen channel was used for a receive, the r_i component is
// set to the received value, where i is the index of that state among
// all n receive states; otherwise r_i has the zero value of type T_i.
// Note that the receive index i is not the same as the state
// index index.
//
// The second component of the triple, recvOk, is a boolean whose value
// is true iff the selected operation was a receive and the receive
// successfully yielded a value.
//
// Pos() returns the ast.SelectStmt.Select.
//
// Example printed form:
//
//	t3 = select nonblocking [<-t0, t1<-t2]
//	t4 = select blocking []
func (b Builder) Select(states []*SelectState, blocking bool) (ret Expr) {
	sp := b.StackSave()
	ops := make([]Expr, len(states))
	for i, s := range states {
		ops[i] = b.chanOp(s)
	}
	var fn Expr
	if blocking {
		fn = b.Pkg.rtFunc("Select")
	} else {
		fn = b.Pkg.rtFunc("TrySelect")
	}
	prog := b.Prog
	tSlice := lastParamType(prog, fn)
	slice := b.selectOpsSlice(tSlice, ops)
	ret = b.Call(fn, slice)
	chosen := b.impl.CreateExtractValue(ret.impl, 0, "")
	recvOK := b.impl.CreateExtractValue(ret.impl, 1, "")
	if !blocking {
		// runtime.TrySelect returns (isel, recvOK, tryOK). recvOK is only meaningful
		// for receives; selection success is reported by tryOK.
		tryOK := b.impl.CreateExtractValue(ret.impl, 2, "")
		chosen = llvm.CreateSelect(b.impl, tryOK, chosen, prog.Val(-1).impl)
	}
	results := []llvm.Value{chosen, recvOK}
	typs := []Type{prog.Int(), prog.Bool()}
	for i, s := range states {
		if !s.Send {
			etyp := b.Prog.Elem(s.Chan.Type)
			typs = append(typs, etyp)
			r := b.Load(Expr{b.impl.CreateExtractValue(ops[i].impl, 1, ""), prog.Pointer(etyp)})
			results = append(results, r.impl)
		}
	}
	b.StackRestore(sp)
	return b.aggregateValue(b.Prog.Struct(typs...), results...)
}

func (b Builder) selectOpsSlice(t Type, ops []Expr) Expr {
	prog := b.Prog
	telem := prog.Index(t)
	opPtr := b.ArrayAlloca(telem, prog.Val(len(ops)))
	for i, op := range ops {
		b.Store(b.Advance(opPtr, prog.Val(i)), op)
	}
	n := llvm.ConstInt(prog.tyInt(), uint64(len(ops)), false)
	return b.unsafeSlice(opPtr, n, n)
}

func lastParamType(prog Program, fn Expr) Type {
	params := fn.raw.Type.(*types.Signature).Params()
	return prog.rawType(params.At(params.Len() - 1).Type())
}

func (b Builder) chanOp(s *SelectState) Expr {
	prog := b.Prog
	var val Expr
	var size Expr
	if s.Send {
		val = b.toPtr(s.Value)
		size = prog.IntVal(prog.SizeOf(s.Value.Type), prog.Int32())
	} else {
		etyp := prog.Elem(s.Chan.Type)
		val = b.Alloc(etyp, false)
		size = prog.IntVal(prog.SizeOf(etyp), prog.Int32())
	}
	send := prog.BoolVal(s.Send)
	typ := b.Prog.rtType("ChanOp")
	return b.aggregateValue(typ, s.Chan.impl, val.impl, size.impl, send.impl)
}

func (b Builder) chanOpWithPayload(s *SelectState, payload Expr) Expr {
	if s == nil || payload.IsNil() {
		panic("ssa: channel operation requires compiler-owned payload storage")
	}
	prog := b.Prog
	var elem Type
	if s.Send {
		elem = s.Value.Type
	} else {
		elem = prog.Elem(s.Chan.Type)
	}
	if !types.Identical(payload.Type.RawType(), prog.Pointer(elem).RawType()) {
		panic("ssa: channel operation payload type mismatch")
	}
	value := Expr{payload.impl, prog.VoidPtr()}
	size := prog.IntVal(prog.SizeOf(elem), prog.Int32())
	return b.aggregateValue(
		prog.rtType("ChanOp"),
		s.Chan.impl,
		value.impl,
		size.impl,
		prog.BoolVal(s.Send).impl,
	)
}

// -----------------------------------------------------------------------------
