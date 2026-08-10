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

// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// MakeFunc implementation.

package reflect

import (
	"sync"
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/ffi"
	llruntime "github.com/goplus/llgo/runtime/internal/runtime"
)

type funcData struct {
	ftyp        *funcType
	tin         []*abi.Type
	tout        []*abi.Type
	fn          func(args []Value) (results []Value)
	nin         int
	invokeCIF   *ffi.Signature
	invokeEntry unsafe.Pointer
}

func MakeFunc(typ Type, fn func(args []Value) (results []Value)) Value {
	if typ.Kind() != Func {
		panic("reflect: call of MakeFunc with non-Func type")
	}
	t := typ.common()
	ftyp := (*funcType)(unsafe.Pointer(t))
	entryArgs := make([]*ffi.Type, 0, len(ftyp.In)+3)
	entryArgs = append(entryArgs, ffi.TypePointer, ffi.TypePointer, ffi.TypePointer)
	for _, in := range ftyp.In {
		entryArgs = appendMakeFuncFFIType(entryArgs, in)
	}
	entryCIF, err := ffi.NewSignature(ffi.TypePointer, entryArgs...)
	if err != nil {
		panic(err)
	}

	invokeCIF, err := ffi.NewSignature(
		ffi.TypePointer,
		ffi.TypePointer, ffi.TypePointer, ffi.TypePointer,
		ffi.TypePointer, ffi.TypePointer, ffi.TypePointer,
	)
	if err != nil {
		panic(err)
	}
	invoker := unpackEface(makeFuncInvokeValue)
	if invoker.Kind() != Func || invoker.ptr == nil {
		panic("reflect: internal error: invalid MakeFunc invoker")
	}
	invokerWords := (*closure)(invoker.ptr)
	if invokerWords.fn == nil || invokerWords.env != nil {
		panic("reflect: internal error: invalid MakeFunc invoker descriptor")
	}

	ins := make([]*abi.Type, len(ftyp.In))
	for i, typ := range ftyp.In {
		ins[i] = typ
		if typ.Kind() == abi.Func {
			ins[i] = closureOf(typ.FuncType())
		}
	}
	outs := toRuntimeTypes(ftyp.Out)
	resultSize, resultAlign := llgoResultLayout(outs)
	closure := ffi.NewClosure()
	userdata := &funcData{
		ftyp:        ftyp,
		tin:         ins,
		fn:          fn,
		nin:         len(ftyp.In),
		tout:        outs,
		invokeCIF:   invokeCIF,
		invokeEntry: ffi.CoroEntry(invokerWords.fn),
	}
	err = closure.Bind(entryCIF, bindCoro, unsafe.Pointer(userdata))
	if err != nil {
		panic("libffi error: " + err.Error())
	}
	descriptor := ffi.NewRuntimeCoroDescriptor(
		unsafe.Pointer(t), closure.Fn, resultSize, resultAlign,
	)

	// keep alive for bdw-gc
	keepMutex.Lock()
	keepAlive = append(
		keepAlive,
		closure, entryCIF, invokeCIF, userdata, descriptor,
	)
	keepMutex.Unlock()

	styp := closureOf(ftyp)
	fv := &struct {
		fn  unsafe.Pointer
		env unsafe.Pointer
	}{descriptor, unsafe.Pointer(userdata)}
	return Value{styp, unsafe.Pointer(fv), flagIndir | flag(Func)}
}

var (
	keepMutex sync.Mutex
	keepAlive []any
)

func appendMakeFuncFFIType(args []*ffi.Type, typ *abi.Type) []*ffi.Type {
	if ffiCallSliceAsTriple && typ.Kind() == abi.Slice {
		return append(args, ffi.TypePointer, ffi.TypeInt, ffi.TypeInt)
	}
	return append(args, toFFIType(typ))
}

// bindCoro is a bounded raw-C callback. It only copies libffi-owned arguments
// into managed storage and invokes the fixed-signature MakeFunc coroutine ramp
// until that ramp returns its initially suspended child handle. No libffi
// frame survives suspension.
func bindCoro(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	env := *(*unsafe.Pointer)(ffi.Index(args, 2))
	if fd == nil || env != userdata {
		*(*unsafe.Pointer)(ret) = nil
		return
	}

	g := *(*unsafe.Pointer)(ffi.Index(args, 0))
	out := *(*unsafe.Pointer)(ffi.Index(args, 1))
	values := copyMakeFuncArgs(fd, args)
	invokerEnv := unsafe.Pointer(nil)
	fdArg := userdata
	outArg := out
	valuesArg := values
	argv := [6]unsafe.Pointer{
		unsafe.Pointer(&g),
		unsafe.Pointer(&out),
		unsafe.Pointer(&invokerEnv),
		unsafe.Pointer(&fdArg),
		unsafe.Pointer(&outArg),
		unsafe.Pointer(&valuesArg),
	}
	ffi.CallRaw(fd.invokeCIF, fd.invokeEntry, ret, &argv[0])
}

func copyMakeFuncArgs(fd *funcData, args *unsafe.Pointer) unsafe.Pointer {
	if fd.nin == 0 {
		return nil
	}
	values := llruntime.AllocZ(uintptr(fd.nin) * unsafe.Sizeof(Value{}))
	ins := unsafe.Slice((*Value)(values), fd.nin)
	index := uintptr(3)
	for i, typ := range fd.ftyp.In {
		if ffiCallSliceAsTriple && typ.Kind() == abi.Slice {
			header := (*unsafeheaderSlice)(llruntime.AllocZ(unsafe.Sizeof(unsafeheaderSlice{})))
			header.Data = *(*unsafe.Pointer)(ffi.Index(args, index))
			header.Len = *(*int)(ffi.Index(args, index+1))
			header.Cap = *(*int)(ffi.Index(args, index+2))
			ins[i] = Value{
				typ_: typ,
				ptr:  unsafe.Pointer(header),
				flag: flag(Slice) | flagIndir,
			}
			index += 3
			continue
		}
		ins[i] = ffiToOwnedValue(ffi.Index(args, index), typ, fd.tin[i])
		index++
	}
	return values
}

// makeFuncInvokeValue forces the compiler to publish makeFuncInvoke through
// the canonical managed descriptor representation. bindCoro consumes only
// that compiler-injected descriptor; it never reverse-maps a raw PC. Assigning
// it from init avoids a Go initialization-dependency cycle now that method
// values reuse MakeFunc.
var makeFuncInvokeValue any

func init() {
	makeFuncInvokeValue = makeFuncInvoke
}

func makeFuncInvoke(fd *funcData, ret, values unsafe.Pointer) {
	var ins []Value
	if fd.nin != 0 {
		ins = unsafe.Slice((*Value)(values), fd.nin)
	}
	outs := validateMakeFuncResults(fd.fn(ins), fd.ftyp, fd.tout)
	if ret == nil {
		return
	}
	var offset uintptr
	for i, out := range outs {
		typ := fd.tout[i]
		size, alignment := llgoABITypeLayout(typ)
		offset = alignUp(offset, alignment)
		storeMakeFuncResult(add(ret, offset, ""), out, typ, size)
		offset += size
	}
}

func validateMakeFuncResults(out []Value, ftyp *abi.FuncType, touts []*abi.Type) []Value {
	if len(out) != len(touts) {
		panic("reflect: wrong return count from function created by MakeFunc")
	}
	for i, typ := range touts {
		v := out[i]
		if v.typ() == nil {
			panic("reflect: function created by MakeFunc returned zero Value")
		}
		if v.flag&flagRO != 0 {
			panic("reflect: function created by MakeFunc returned value obtained from unexported field")
		}
		out[i] = v.assignTo("reflect: function created by MakeFunc", typ, nil)
	}
	return out
}

func storeMakeFuncResult(ret unsafe.Pointer, v Value, typ *abi.Type, size uintptr) {
	if size == 0 {
		return
	}
	c.Memmove(ret, toFFIArg(v, typ), size)
}

func ffiToOwnedValue(ptr unsafe.Pointer, typ, storageType *abi.Type) (v Value) {
	kind := typ.Kind()
	v.typ_ = storageType
	v.flag = flag(kind)
	if storageType.IfaceIndir() {
		v.flag |= flagIndir
		size, _ := llgoABITypeLayout(storageType)
		allocSize := storageType.Size_
		if size > allocSize {
			allocSize = size
		}
		if allocSize == 0 {
			allocSize = 1
		}
		v.ptr = llruntime.AllocZ(allocSize)
		if size != 0 {
			c.Memmove(v.ptr, ptr, size)
		}
	} else {
		v.ptr = *(*unsafe.Pointer)(ptr)
	}
	return
}

/*
import (
	"unsafe"
)

// makeFuncImpl is the closure value implementing the function
// returned by MakeFunc.
// The first three words of this type must be kept in sync with
// methodValue and runtime.reflectMethodValue.
// Any changes should be reflected in all three.
type makeFuncImpl struct {
	makeFuncCtxt
	ftyp *funcType
	fn   func([]Value) []Value
}

// MakeFunc returns a new function of the given Type
// that wraps the function fn. When called, that new function
// does the following:
//
//   - converts its arguments to a slice of Values.
//   - runs results := fn(args).
//   - returns the results as a slice of Values, one per formal result.
//
// The implementation fn can assume that the argument Value slice
// has the number and type of arguments given by typ.
// If typ describes a variadic function, the final Value is itself
// a slice representing the variadic arguments, as in the
// body of a variadic function. The result Value slice returned by fn
// must have the number and type of results given by typ.
//
// The Value.Call method allows the caller to invoke a typed function
// in terms of Values; in contrast, MakeFunc allows the caller to implement
// a typed function in terms of Values.
//
// The Examples section of the documentation includes an illustration
// of how to use MakeFunc to build a swap function for different types.
func MakeFunc(typ Type, fn func(args []Value) (results []Value)) Value {
	if typ.Kind() != Func {
		panic("reflect: call of MakeFunc with non-Func type")
	}

	t := typ.common()
	ftyp := (*funcType)(unsafe.Pointer(t))

	code := abi.FuncPCABI0(makeFuncStub)

	// makeFuncImpl contains a stack map for use by the runtime
	_, _, abid := funcLayout(ftyp, nil)

	impl := &makeFuncImpl{
		makeFuncCtxt: makeFuncCtxt{
			fn:      code,
			stack:   abid.stackPtrs,
			argLen:  abid.stackCallArgsSize,
			regPtrs: abid.inRegPtrs,
		},
		ftyp: ftyp,
		fn:   fn,
	}

	return Value{t, unsafe.Pointer(impl), flag(Func)}
}

// makeFuncStub is an assembly function that is the code half of
// the function returned from MakeFunc. It expects a *callReflectFunc
// as its context register, and its job is to invoke callReflect(ctxt, frame)
// where ctxt is the context register and frame is a pointer to the first
// word in the passed-in argument frame.
func makeFuncStub()

// The first 3 words of this type must be kept in sync with
// makeFuncImpl and runtime.reflectMethodValue.
// Any changes should be reflected in all three.
type methodValue struct {
	makeFuncCtxt
	method int
	rcvr   Value
}
*/

// makeMethodValue converts v from the rcvr+method index representation
// of a method value to an actual method func value, which is
// basically the receiver value with a special bit set, into a true
// func value - a value holding an actual func. The output is
// semantically equivalent to the input as far as the user of package
// reflect can tell, but the true func representation can be handled
// by code like Convert and Interface and Assign.
func makeMethodValue(op string, v Value) Value {
	if v.flag&flagMethod == 0 {
		panic("reflect: internal error: invalid use of makeMethodValue")
	}

	rcvr, method := methodExpressionValue(op, v, int(v.flag)>>flagMethodShift)
	// A method value evaluates its receiver once. Boxing and unpacking it here
	// copies value receivers while retaining pointer receiver identity.
	rcvr = ValueOf(valueInterface(rcvr, false))
	ftyp := v.Type()
	variadic := ftyp.common().FuncType().Variadic()
	bound := MakeFunc(ftyp, func(args []Value) []Value {
		in := make([]Value, len(args)+1)
		in[0] = rcvr
		copy(in[1:], args)
		if variadic {
			return method.CallSlice(in)
		}
		return method.Call(in)
	})
	bound.flag |= v.flag & flagRO
	return bound
}

// methodExpressionValue converts the receiver+method-index representation
// used by Value.Method into the ordinary method expression stored in
// abi.Method.Tfn_. Tfn_ is a managed function descriptor whose receiver is an
// explicit first argument. This deliberately avoids treating the legacy Ifn_
// word as a descriptor: Ifn_ may still be a raw one-word-receiver entry for
// method families which have no managed interface invoke.
func methodExpressionValue(op string, v Value, methodIndex int) (Value, Value) {
	fl := v.flag & (flagRO | flagAddr | flagIndir)
	fl |= flag(v.typ().Kind())
	rcvr := Value{v.typ(), v.ptr, fl}

	if rcvr.typ().Kind() == abi.Interface {
		tt := (*interfaceType)(unsafe.Pointer(rcvr.typ()))
		if uint(methodIndex) >= uint(len(tt.Methods)) {
			panic("reflect: internal error: invalid method index")
		}
		m := &tt.Methods[methodIndex]
		if !abi.IsExported(m.Name()) {
			panic("reflect: " + op + " of unexported method")
		}
		iface := (*nonEmptyInterface)(rcvr.ptr)
		if iface.itab == nil {
			panic("reflect: " + op + " of method on nil interface value")
		}
		rcvr = rcvr.Elem()
		method, ok := toRType(rcvr.typ()).MethodByName(m.Name())
		if !ok {
			panic("reflect: internal error: dynamic method is absent from concrete type")
		}
		return rcvr, method.Func
	}

	methods := rcvr.typ().ExportedMethods()
	if uint(methodIndex) >= uint(len(methods)) {
		panic("reflect: internal error: invalid method index")
	}
	method := toRType(rcvr.typ()).Method(methodIndex)
	return rcvr, method.Func
}

var unsafePointerType = rtypeOf(unsafe.Pointer(nil))

/*
func methodValueCallCodePtr() uintptr {
	return abi.FuncPCABI0(methodValueCall)
}

// methodValueCall is an assembly function that is the code half of
// the function returned from makeMethodValue. It expects a *methodValue
// as its context register, and its job is to invoke callMethod(ctxt, frame)
// where ctxt is the context register and frame is a pointer to the first
// word in the passed-in argument frame.
func methodValueCall()

// This structure must be kept in sync with runtime.reflectMethodValue.
// Any changes should be reflected in all both.
type makeFuncCtxt struct {
	fn      uintptr
	stack   *bitVector // ptrmap for both stack args and results
	argLen  uintptr    // just args
	regPtrs abi.IntArgRegBitmap
}

// moveMakeFuncArgPtrs uses ctxt.regPtrs to copy integer pointer arguments
// in args.Ints to args.Ptrs where the GC can see them.
//
// This is similar to what reflectcallmove does in the runtime, except
// that happens on the return path, whereas this happens on the call path.
//
// nosplit because pointers are being held in uintptr slots in args, so
// having our stack scanned now could lead to accidentally freeing
// memory.
//
//go:nosplit
func moveMakeFuncArgPtrs(ctxt *makeFuncCtxt, args *abi.RegArgs) {
	for i, arg := range args.Ints {
		// Avoid write barriers! Because our write barrier enqueues what
		// was there before, we might enqueue garbage.
		if ctxt.regPtrs.Get(i) {
			*(*uintptr)(unsafe.Pointer(&args.Ptrs[i])) = arg
		} else {
			// We *must* zero this space ourselves because it's defined in
			// assembly code and the GC will scan these pointers. Otherwise,
			// there will be garbage here.
			*(*uintptr)(unsafe.Pointer(&args.Ptrs[i])) = 0
		}
	}
}
*/
