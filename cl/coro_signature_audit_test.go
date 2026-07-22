//go:build !llgo

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
	"go/types"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// This test freezes the x/tools SSA operand shapes used by coroutine ABI
// normalization. In particular, a declared receiver is a Function.Param and a
// direct-call argument even though go/types keeps it outside Signature.Params.
// Interface receivers and closure bindings use different SSA fields and must
// not be folded into the same signature-only rule.
func TestCoroPhysicalABISSAOperandShapeAudit(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo

type Counter struct { value int }
func (counter *Counter) Add(delta int) int { return counter.value + delta }

type Adder interface { Add(int) int }
func Direct(counter *Counter) int { return counter.Add(2) }
func Invoke(adder Adder) int { return adder.Add(3) }

func CallClosure(base int) int {
	add := func(delta int) int { return base + delta }
	return add(4)
}

func Recursive(value int) int {
	if value == 0 { return 0 }
	return Recursive(value - 1) + 1
}

func Heap() *Counter { return &Counter{} }
`)

	method := coroSignatureAuditDeclaredMethod(t, pkg)
	sig := method.Signature
	if sig == nil || sig.Recv() == nil || sig.Params().Len() != 1 {
		t.Fatalf("declared method signature = %v", sig)
	}
	if got, want := len(method.Params), sig.Params().Len()+1; got != want {
		t.Fatalf("method SSA params = %d, want receiver + signature params = %d", got, want)
	}
	if !types.Identical(method.Params[0].Type(), sig.Recv().Type()) {
		t.Fatalf("method first SSA param %s != receiver %s", method.Params[0].Type(), sig.Recv().Type())
	}
	for index := 0; index < sig.Params().Len(); index++ {
		if !types.Identical(method.Params[index+1].Type(), sig.Params().At(index).Type()) {
			t.Fatalf("method SSA param %d %s != signature param %d %s", index+1, method.Params[index+1].Type(), index, sig.Params().At(index).Type())
		}
	}

	// LLGo already uses this receiver-to-leading-parameter normalization for
	// ordinary Go declarations. A coroutine signature projection can reuse the
	// same ordering without changing the immutable source Signature or Params.
	normalized := llssa.FuncAddCtx(sig.Recv(), sig)
	if normalized.Recv() != nil || normalized.Params().Len() != len(method.Params) {
		t.Fatalf("normalized method signature = %v, SSA params = %d", normalized, len(method.Params))
	}
	for index, parameter := range method.Params {
		if !types.Identical(normalized.Params().At(index).Type(), parameter.Type()) {
			t.Fatalf("normalized param %d %s != SSA param %s", index, normalized.Params().At(index).Type(), parameter.Type())
		}
	}

	direct := pkg.Func("Direct")
	directCall := coroSignatureAuditOnlyCall(t, direct, func(call *ssa.Call) bool {
		return call.Common().StaticCallee() == method
	})
	if directCall.Common().IsInvoke() || len(directCall.Common().Args) != len(method.Params) {
		t.Fatalf("direct method call = %s; args=%d method-params=%d", directCall, len(directCall.Common().Args), len(method.Params))
	}
	if directCall.Common().Args[0] != direct.Params[0] {
		t.Fatalf("direct method receiver is not call argument zero: %s", directCall)
	}

	invoke := pkg.Func("Invoke")
	invokeCall := coroSignatureAuditOnlyCall(t, invoke, func(call *ssa.Call) bool {
		return call.Common().IsInvoke()
	})
	if invokeCall.Common().Value != invoke.Params[0] || invokeCall.Common().Method == nil {
		t.Fatalf("interface receiver/method are not carried by CallCommon: %+v", invokeCall.Common())
	}
	if got := len(invokeCall.Common().Args); got != 1 {
		t.Fatalf("interface invoke args = %d, want only the explicit delta argument", got)
	}

	closureOwner := pkg.Func("CallClosure")
	if len(closureOwner.AnonFuncs) != 1 {
		t.Fatalf("CallClosure anonymous functions = %d, want one", len(closureOwner.AnonFuncs))
	}
	closure := closureOwner.AnonFuncs[0]
	if closure.Parent() != closureOwner || len(closure.Params) != 1 || len(closure.FreeVars) != 1 {
		t.Fatalf("closure shape: parent=%v params=%d free-vars=%d", closure.Parent(), len(closure.Params), len(closure.FreeVars))
	}
	makeClosure := coroSignatureAuditOnlyMakeClosure(t, closureOwner)
	if makeClosure.Fn != closure || len(makeClosure.Bindings) != len(closure.FreeVars) {
		t.Fatalf("MakeClosure shape = %s; bindings=%d free-vars=%d", makeClosure, len(makeClosure.Bindings), len(closure.FreeVars))
	}
	closureCall := coroSignatureAuditOnlyCall(t, closureOwner, func(call *ssa.Call) bool {
		return call.Common().StaticCallee() == closure
	})
	if closureCall.Common().Value != makeClosure || len(closureCall.Common().Args) != len(closure.Params) {
		t.Fatalf("closure call = %s; value=%T args=%d params=%d", closureCall, closureCall.Common().Value, len(closureCall.Common().Args), len(closure.Params))
	}
	if closureCall.Common().Args[0] == makeClosure.Bindings[0] {
		t.Fatal("closure binding was incorrectly duplicated into ordinary call arguments")
	}

	recursive := pkg.Func("Recursive")
	recursiveCall := coroSignatureAuditOnlyCall(t, recursive, func(call *ssa.Call) bool {
		return call.Common().StaticCallee() == recursive
	})
	if len(recursiveCall.Common().Args) != len(recursive.Params) {
		t.Fatalf("recursive call args=%d params=%d", len(recursiveCall.Common().Args), len(recursive.Params))
	}

	heap := pkg.Func("Heap")
	allocations := 0
	for _, block := range heap.Blocks {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok && alloc.Heap {
				allocations++
			}
		}
	}
	if allocations != 1 {
		t.Fatalf("Heap escaping SSA allocations = %d, want one", allocations)
	}
}

func coroSignatureAuditDeclaredMethod(t *testing.T, pkg *ssa.Package) *ssa.Function {
	t.Helper()
	var matches []*ssa.Function
	for function := range ssautil.AllFunctions(pkg.Prog) {
		if function != nil && function.Name() == "Add" && function.Signature != nil && function.Signature.Recv() != nil && function.Object() != nil {
			matches = append(matches, function)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("declared Add methods = %d, want one", len(matches))
	}
	return matches[0]
}

func coroSignatureAuditOnlyCall(t *testing.T, function *ssa.Function, match func(*ssa.Call) bool) *ssa.Call {
	t.Helper()
	var matches []*ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && match(call) {
				matches = append(matches, call)
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s matching calls = %d, want one", function, len(matches))
	}
	return matches[0]
}

func coroSignatureAuditOnlyMakeClosure(t *testing.T, function *ssa.Function) *ssa.MakeClosure {
	t.Helper()
	var matches []*ssa.MakeClosure
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if closure, ok := instruction.(*ssa.MakeClosure); ok {
				matches = append(matches, closure)
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s MakeClosure instructions = %d, want one", function, len(matches))
	}
	return matches[0]
}
