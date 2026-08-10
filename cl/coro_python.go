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

package cl

import (
	"fmt"
	"go/types"
	"strconv"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroPythonCallThunkPrefixV1 = "__llgo_coro_python_call_thunk_v1_"

// freezeCoroErasedPythonFunctionInterface records the exact x/tools box that
// LLGo's __llgo_va_list lowering consumes as a concrete Python object. The Go
// interface never exists physically, so it must not force a managed function
// descriptor; the static Python function reference itself remains visible to
// demand analysis.
func (u *EmissionUniverse) freezeCoroErasedPythonFunctionInterface(
	ctx *context,
	box *ssa.MakeInterface,
) error {
	if u == nil || u.coroProgramIR == nil || ctx == nil || box == nil || box.X == nil {
		return nil
	}
	raw, exact := box.X.(*ssa.Function)
	if !exact || raw == nil {
		return nil
	}
	target, frozen := u.Resolve(raw)
	if !frozen || target == nil {
		return nil
	}
	background, classified, err := u.FunctionBackground(target)
	if err != nil {
		return fmt.Errorf("classify erased Python function interface: %w", err)
	}
	if !classified || background != llssa.InPython {
		return nil
	}
	refs := box.Referrers()
	if refs == nil || len(*refs) != 1 {
		return nil
	}
	store, ok := (*refs)[0].(*ssa.Store)
	if !ok || store.Val != box {
		return nil
	}
	address, ok := store.Addr.(*ssa.IndexAddr)
	if !ok || !emissionIsVargsAlloc(ctx, address.X) {
		return nil
	}
	u.coroProgramIR.erasedFunctionInterfaces[box] = none{}
	return nil
}

// CoroErasedFunctionInterface is the immutable analyzer projection for one
// compiler-elided Python function box.
func (u *EmissionUniverse) CoroErasedFunctionInterface(box *ssa.MakeInterface) (bool, error) {
	if u == nil || u.coroProgramIR == nil {
		return false, fmt.Errorf("emission universe has no coroutine ProgramIR")
	}
	return u.coroProgramIR.erasedFunctionInterface(box)
}

// compileCoroPythonOperation lowers one source-level Python operation selected
// by ProgramIR. The high-level operation stays synchronous in Go source; every
// concrete C-API call it expands into is intercepted by resolveCoroPythonCall
// and executed in a typed same-M foreign episode.
func (p *context) compileCoroPythonOperation(
	b llssa.Builder,
	call *ssa.Call,
	target *ssa.Function,
	opcode int,
) llssa.Expr {
	if p == nil || call == nil || call.Common() == nil ||
		!p.hasStructuredOutcomePhysicalBody() || p.goFn == nil ||
		!isCoroProgramManagedEntry(p.goFn) {
		panic("Python operation requires one physical program-root coroutine call")
	}
	if target != nil {
		if opcode != 0 {
			panic("Python frontend operation carries an intrinsic opcode")
		}
		_, pyFn, kind := p.compileFunction(target)
		if kind != pyFunc || pyFn == nil {
			panic("Python operation lost its exact frontend function")
		}
		args := p.compileValues(b, call.Common().Args, p.funcKind(target))
		return b.Call(pyFn.Expr, args...)
	}
	switch opcode {
	case llgoPyList:
		args := p.compileValues(b, call.Common().Args, fnHasVArg)
		return b.PyList(args...)
	case llgoPyTuple:
		args := p.compileValues(b, call.Common().Args, fnHasVArg)
		return b.PyTuple(args...)
	case llgoPyStr:
		return pystr(b, call.Common().Args)
	default:
		panic(fmt.Sprintf("unsupported Python operation opcode %d", opcode))
	}
}

// resolveCoroPythonCall is the sole LLSSA boundary for compiler-generated
// Python C-API calls.  A physical program-root coroutine executes the typed
// call on its current native M while the ordinary same-M foreign episode lends
// the P to a replacement M.  Plain builds retain the historical direct call.
//
// Source-level authorization remains in CoroProgramIR.  This resolver only
// supplies the final typed mechanics for calls generated while lowering that
// authorized operation (PyObject_Call*, conversions, and module-symbol load).
func (p *context) resolveCoroPythonCall(
	b llssa.Builder,
	fn llssa.Expr,
	args []llssa.Expr,
) (llssa.Expr, bool) {
	if p == nil || p.inCoroPythonThunk || !p.hasStructuredOutcomePhysicalBody() {
		return llssa.Expr{}, false
	}
	if p.goFn == nil || !isCoroProgramManagedEntry(p.goFn) {
		owner := "<nil>"
		if p.goFn != nil {
			owner = p.goFn.Name()
		}
		panic(fmt.Errorf(
			"Python C-API call in %q has no compiler-owned program-root owner realm",
			owner,
		))
	}
	signature, ok := fn.RawType().(*types.Signature)
	if !ok || signature == nil || signature.Recv() != nil ||
		coroWorkerTypeParamLen(signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(signature.RecvTypeParams()) != 0 {
		panic("coroutine Python call lost its exact C signature")
	}
	results := signature.Results()
	if results != nil && results.Len() > 1 {
		panic("coroutine Python call supports at most one physical result")
	}

	fields := make([]llssa.Type, 0, len(args)+1)
	keyFields := make([]string, 0, len(args)+5)
	keyFields = append(keyFields,
		"cl-coro-python-call-thunk-v1",
		fn.Name(),
		structuralEmissionABITypeKey(signature),
		strconv.Itoa(p.prog.PointerSize()),
	)
	for _, argument := range args {
		if argument.IsNil() || argument.Type == nil {
			panic("coroutine Python call has an untyped physical argument")
		}
		fields = append(fields, argument.Type)
		keyFields = append(keyFields, structuralEmissionABITypeKey(argument.RawType()))
	}
	resultField := -1
	if results != nil && results.Len() == 1 {
		resultField = len(fields)
		resultType := p.prog.Type(results.At(0).Type(), llssa.InC)
		fields = append(fields, resultType)
		keyFields = append(keyFields, structuralEmissionABITypeKey(resultType.RawType()))
	}
	recordType := p.prog.Struct(fields...)
	name := coroPythonCallThunkPrefixV1 + emissionDigest(framedEmissionKey(keyFields...))
	thunk := p.pkg.NewFuncEx(
		name,
		coroWorkerForeignThunkSignature(),
		llssa.InC,
		false,
		true,
	)
	if !thunk.HasBody() {
		p.inCoroPythonThunk = true
		func() {
			defer func() { p.inCoroPythonThunk = false }()
			body := thunk.MakeBody(1)
			record := body.Convert(p.prog.Pointer(recordType), thunk.Param(0))
			callArgs := make([]llssa.Expr, len(args))
			for index := range callArgs {
				callArgs[index] = body.LoadKnownNonNil(body.FieldAddr(record, index))
			}
			result := body.Call(fn, callArgs...)
			if resultField >= 0 {
				body.Store(body.FieldAddr(record, resultField), result)
			}
			body.Return(p.prog.Zero(p.prog.Uintptr()))
			body.EndBuild()
			body.Dispose()
		}()
	}

	record := p.coroFrameAlloc(recordType)
	for index, argument := range args {
		b.Store(b.FieldAddr(record, index), argument)
	}
	task := p.coroTask()
	if task.IsNil() {
		panic("coroutine Python call has no active physical task")
	}
	invoke := p.pkg.NewFunc(
		coroSameMForeignCallHookV1,
		coroSameMForeignCallSignature(),
		llssa.InC,
	)
	b.Call(
		invoke.Expr,
		task,
		b.Convert(p.prog.Uintptr(), thunk.Expr),
		b.Convert(p.prog.Uintptr(), record),
	)
	b.KeepAlive(record)
	if resultField < 0 {
		return llssa.Expr{}, true
	}
	return b.LoadKnownNonNil(b.FieldAddr(record, resultField)), true
}
