//go:build windows

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
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

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	psync "github.com/goplus/llgo/runtime/internal/clite/sync"
	"github.com/goplus/llgo/runtime/internal/ffi"
	llruntime "github.com/goplus/llgo/runtime/internal/runtime"
)

const windowsCallbackMaxFrame = 64 * unsafe.Sizeof(uintptr(0))

type windowsCallbackFunc struct {
	code unsafe.Pointer
	env  unsafe.Pointer
}

type windowsCallbackKey struct {
	fn         unsafe.Pointer
	cleanstack bool
}

type windowsCallbackEntry struct {
	fnValue     any
	fn          *windowsCallbackFunc
	closure     *ffi.Closure
	goSig       *ffi.Signature
	cSig        *ffi.Signature
	explicitEnv bool

	// Both signatures retain pointers into these backing arrays.
	goArgTypes []*ffi.Type
	cArgTypes  []*ffi.Type
	// cArgIndex maps each Go parameter to its C argument, or -1 for a
	// zero-sized parameter that consumes no Windows ABI argument slot.
	cArgIndex []int
}

var windowsCallbacks struct {
	once psync.Once
	mu   psync.Mutex
	m    map[windowsCallbackKey]*windowsCallbackEntry
}

func initWindowsCallbacks() {
	windowsCallbacks.mu.Init(nil)
	windowsCallbacks.m = make(map[windowsCallbackKey]*windowsCallbackEntry)
}

func windowsCallbackFuncType(t *abi.Type) *abi.FuncType {
	if t == nil || !t.IsClosure() {
		return nil
	}
	st := t.StructType()
	if st == nil || len(st.Fields) == 0 {
		return nil
	}
	return st.Fields[0].Typ.FuncType()
}

func windowsCallbackABI(cleanstack bool) ffi.ABI {
	if GOARCH == "386" && cleanstack {
		return ffi.WindowsStdcallABI
	}
	return ffi.DefaultABI
}

func windowsCallbackFFIType(t *abi.Type) *ffi.Type {
	if t.Size_ == 0 {
		return ffi.StructOf()
	}
	if t.Size_ > unsafe.Sizeof(uintptr(0)) {
		panic("compileCallback: argument size is larger than uintptr")
	}
	switch t.Kind() {
	case abi.Bool:
		return ffi.TypeBool
	case abi.Int:
		return ffi.TypeInt
	case abi.Int8:
		return ffi.TypeInt8
	case abi.Int16:
		return ffi.TypeInt16
	case abi.Int32:
		return ffi.TypeInt32
	case abi.Int64:
		return ffi.TypeInt64
	case abi.Uint:
		return ffi.TypeUint
	case abi.Uint8:
		return ffi.TypeUint8
	case abi.Uint16:
		return ffi.TypeUint16
	case abi.Uint32:
		return ffi.TypeUint32
	case abi.Uint64:
		return ffi.TypeUint64
	case abi.Uintptr:
		return ffi.TypeUintptr
	case abi.Float32:
		if GOARCH != "386" {
			panic("compileCallback: float arguments not supported")
		}
		return ffi.TypeFloat32
	case abi.Float64:
		if GOARCH != "386" {
			panic("compileCallback: float arguments not supported")
		}
		return ffi.TypeFloat64
	case abi.Pointer, abi.UnsafePointer:
		return ffi.TypePointer
	case abi.Array:
		at := t.ArrayType()
		if at.Len == 1 {
			return ffi.ArrayOf(windowsCallbackFFIType(at.Elem), 1)
		}
	case abi.Struct:
		return windowsCallbackFFIStructType(t)
	}
	panic("compileCallback: type " + t.String() + " is currently not supported for use in system callbacks")
}

func windowsCallbackFFIStructType(t *abi.Type) *ffi.Type {
	st := t.StructType()
	fields := make([]*ffi.Type, 0, len(st.Fields))
	var off uintptr
	for _, field := range st.Fields {
		if field.Offset > off {
			fields, off = appendWindowsCallbackFFIPadding(fields, off, field.Offset-off)
		}
		if field.Typ.Size_ == 0 {
			continue
		}
		fields = append(fields, windowsCallbackFFIType(field.Typ))
		off = field.Offset + field.Typ.Size_
	}
	return ffi.StructOf(fields...)
}

func appendWindowsCallbackFFIPadding(fields []*ffi.Type, off, size uintptr) ([]*ffi.Type, uintptr) {
	for size > 0 {
		switch {
		case off%8 == 0 && size >= 8:
			fields = append(fields, ffi.TypeUint64)
			off += 8
			size -= 8
		case off%4 == 0 && size >= 4:
			fields = append(fields, ffi.TypeUint32)
			off += 4
			size -= 4
		case off%2 == 0 && size >= 2:
			fields = append(fields, ffi.TypeUint16)
			off += 2
			size -= 2
		default:
			fields = append(fields, ffi.TypeUint8)
			off++
			size--
		}
	}
	return fields, off
}

func windowsCallbackResultFFIType(t *abi.Type) *ffi.Type {
	switch t.Kind() {
	case abi.Bool:
		return ffi.TypeBool
	case abi.Int:
		return ffi.TypeInt
	case abi.Int8:
		return ffi.TypeInt8
	case abi.Int16:
		return ffi.TypeInt16
	case abi.Int32:
		return ffi.TypeInt32
	case abi.Int64:
		return ffi.TypeInt64
	case abi.Uint:
		return ffi.TypeUint
	case abi.Uint8:
		return ffi.TypeUint8
	case abi.Uint16:
		return ffi.TypeUint16
	case abi.Uint32:
		return ffi.TypeUint32
	case abi.Uint64:
		return ffi.TypeUint64
	case abi.Uintptr:
		return ffi.TypeUintptr
	case abi.Pointer, abi.UnsafePointer, abi.Chan, abi.Map:
		return ffi.TypePointer
	case abi.Array:
		at := t.ArrayType()
		return ffi.ArrayOf(windowsCallbackResultFFIType(at.Elem), int(at.Len))
	case abi.Struct:
		return windowsCallbackFFIStructType(t)
	}
	panic("compileCallback: type " + t.String() + " is currently not supported for use in system callbacks")
}

func newWindowsCallbackEntry(fn any, fnFace *eface, ft *abi.FuncType, cleanstack bool) *windowsCallbackEntry {
	goArgTypes := make([]*ffi.Type, 0, len(ft.In)+1)
	explicitEnv := false
	callbackFn := (*windowsCallbackFunc)(fnFace.data)
	if callbackFn == nil || callbackFn.code == nil {
		panic("compileCallback: expected function with one uintptr-sized result")
	}
	if callbackFn.env != nil && ffi.ClosureEnvExplicit {
		explicitEnv = true
		goArgTypes = append(goArgTypes, ffi.TypePointer)
	}

	cArgTypes := make([]*ffi.Type, 0, len(ft.In))
	cArgIndex := make([]int, len(ft.In))
	frameSize := uintptr(0)
	for i, t := range ft.In {
		argType := windowsCallbackFFIType(t)
		goArgTypes = append(goArgTypes, argType)
		if t.Size_ == 0 {
			cArgIndex[i] = -1
			continue
		}
		cArgIndex[i] = len(cArgTypes)
		cArgTypes = append(cArgTypes, argType)
		frameSize += unsafe.Sizeof(uintptr(0))
	}
	if frameSize > windowsCallbackMaxFrame {
		panic("compileCallback: function argument frame too large")
	}

	if len(ft.Out) != 1 || ft.Out[0].Size_ != unsafe.Sizeof(uintptr(0)) {
		panic("compileCallback: expected function with one uintptr-sized result")
	}
	if kind := ft.Out[0].Kind(); kind == abi.Float32 || kind == abi.Float64 {
		panic("compileCallback: float results not supported")
	}
	goResultType := windowsCallbackResultFFIType(ft.Out[0])
	goSig, err := ffi.NewSignature(goResultType, goArgTypes...)
	if err != nil {
		panic("libffi error: " + err.Error())
	}
	cSig, err := ffi.NewSignatureWithABI(windowsCallbackABI(cleanstack), ffi.TypeUintptr, cArgTypes...)
	if err != nil {
		panic("libffi error: " + err.Error())
	}

	entry := &windowsCallbackEntry{
		fnValue:     fn,
		fn:          callbackFn,
		goSig:       goSig,
		cSig:        cSig,
		explicitEnv: explicitEnv,
		goArgTypes:  goArgTypes,
		cArgTypes:   cArgTypes,
		cArgIndex:   cArgIndex,
	}
	entry.closure = ffi.NewClosure()
	if entry.closure.Fn == nil {
		panic("libffi error: closure allocation failed")
	}
	if err := entry.closure.Bind(cSig, callWindowsCallback, unsafe.Pointer(entry)); err != nil {
		entry.closure.Free()
		panic("libffi error: " + err.Error())
	}
	return entry
}

func callWindowsCallback(_ *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	registered := llruntime.EnterForeignThread()
	defer llruntime.ExitForeignThread(registered)

	entry := (*windowsCallbackEntry)(userdata)
	var localArgs [65]unsafe.Pointer // 64 callback words plus an explicit env.
	goArgs := localArgs[:0]
	if len(entry.goArgTypes) > len(localArgs) {
		goArgs = make([]unsafe.Pointer, 0, len(entry.goArgTypes))
	}
	if entry.explicitEnv {
		goArgs = append(goArgs, unsafe.Pointer(&entry.fn.env))
	}
	var zero byte
	for _, index := range entry.cArgIndex {
		if index < 0 {
			goArgs = append(goArgs, unsafe.Pointer(&zero))
		} else {
			goArgs = append(goArgs, ffi.Index(args, uintptr(index)))
		}
	}

	var result uintptr
	ffi.CallWithEnv(entry.goSig, entry.fn.code, entry.fn.env, unsafe.Pointer(&result), goArgs...)
	*(*uintptr)(ret) = result
	KeepAlive(entry.fnValue)
	KeepAlive(entry.goArgTypes)
	KeepAlive(entry.cArgTypes)
}

// syscall_compileCallback converts a Go function to a Windows C callback.
// NewCallback's cleanstack argument selects stdcall only on windows/386;
// amd64 and arm64 each have a single native calling convention.
//
//go:linkname syscall_compileCallback syscall.compileCallback
func syscall_compileCallback(fn any, cleanstack bool) uintptr {
	fnFace := (*eface)(unsafe.Pointer(&fn))
	ft := windowsCallbackFuncType(fnFace._type)
	if ft == nil {
		panic("compileCallback: expected function with one uintptr-sized result")
	}
	if GOARCH != "386" {
		cleanstack = false
	}
	key := windowsCallbackKey{fn: fnFace.data, cleanstack: cleanstack}

	windowsCallbacks.once.Do(initWindowsCallbacks)
	windowsCallbacks.mu.Lock()
	if entry := windowsCallbacks.m[key]; entry != nil {
		code := uintptr(entry.closure.Fn)
		windowsCallbacks.mu.Unlock()
		return code
	}
	windowsCallbacks.mu.Unlock()

	entry := newWindowsCallbackEntry(fn, fnFace, ft, cleanstack)
	windowsCallbacks.mu.Lock()
	if existing := windowsCallbacks.m[key]; existing != nil {
		code := uintptr(existing.closure.Fn)
		windowsCallbacks.mu.Unlock()
		entry.closure.Free()
		return code
	}
	windowsCallbacks.m[key] = entry
	windowsCallbacks.mu.Unlock()
	return uintptr(entry.closure.Fn)
}
