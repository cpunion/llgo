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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroUniqueAsyncWriterSource = `package foo

var gate chan struct{}

type Writer interface { Write([]byte) (int, error) }
type AsyncWriter struct{}

func (*AsyncWriter) Write(buffer []byte) (int, error) {
	<-gate
	return len(buffer), nil
}

func Root(writer Writer) (int, error) {
	return writer.Write([]byte("payload"))
}
`

func TestResolveCoroInterfaceDispatchPlanUniqueAsyncWriter(t *testing.T) {
	fixture := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.call != fixture.invoke || resolved.receiver != fixture.invoke.Common().Value || resolved.method.Id() != "Write" {
		t.Fatalf("resolved call facts do not preserve the exact invoke: %+v", resolved)
	}
	if !resolved.mayBeNil {
		t.Fatal("interface invoke lost its required nil-interface panic check")
	}
	if resolved.sourceCallSignature == nil || resolved.sourceCallSignature.Recv() != nil || resolved.sourceCallSignature.Variadic() ||
		resolved.sourceCallSignature.Params().Len() != 1 || resolved.sourceCallSignature.Results().Len() != 2 {
		t.Fatalf("source call signature = %v", resolved.sourceCallSignature)
	}
	if len(resolved.candidates) != 1 {
		t.Fatalf("candidates = %d, want one: %+v", len(resolved.candidates), resolved.candidates)
	}
	candidate := resolved.candidates[0]
	if candidate.function == nil || candidate.function.Name() != "Write" || candidate.plan.ID != candidate.id ||
		candidate.plan.External != coro.Defined || candidate.plan.Emission != coro.EmitCoroutine ||
		candidate.plan.Primary != coro.PrimaryCoroutine || candidate.plan.Demand != coro.AsyncDemand ||
		candidate.plan.FuncRep != coro.Dispatch || !candidate.plan.Effect.MaySuspend() {
		t.Fatalf("async Writer.Write candidate = %+v", candidate)
	}

	again, err := resolveCoroInterfaceDispatchPlan(fixture.plan, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.candidates) != 1 || again.candidates[0].id != candidate.id || again.candidates[0].function != candidate.function ||
		!types.Identical(again.sourceCallSignature, resolved.sourceCallSignature) {
		t.Fatalf("repeated resolution is not stable: first=%+v again=%+v", resolved, again)
	}
}

func TestResolveCoroInterfaceDispatchPlanMixedPlainAndCoroutine(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type Writer interface { Write([]byte) (int, error) }
type AsyncWriter struct{}
type PlainWriter struct{}
func (*AsyncWriter) Write(buffer []byte) (int, error) { <-gate; return len(buffer), nil }
func (*PlainWriter) Write(buffer []byte) (int, error) { return len(buffer), nil }
func KeepBoth(flag bool) Writer {
	if flag { return &AsyncWriter{} }
	return &PlainWriter{}
}
func Root(writer Writer) (int, error) { return writer.Write([]byte("payload")) }
`
	fixture := buildCoroInterfaceDispatchFixture(t, source, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.candidates) != 2 {
		t.Fatalf("candidates = %d, want mixed pair: %+v", len(resolved.candidates), resolved.candidates)
	}
	plain, asynchronous := 0, 0
	for index, candidate := range resolved.candidates {
		if index != 0 && resolved.candidates[index-1].id >= candidate.id {
			t.Fatalf("candidates are not in strict FunctionID order: %+v", resolved.candidates)
		}
		switch candidate.plan.Emission {
		case coro.EmitPlain:
			plain++
			if candidate.plan.Primary != coro.PrimaryPlain || candidate.plan.Effect != coro.NoSuspend {
				t.Fatalf("plain candidate = %+v", candidate)
			}
		case coro.EmitCoroutine:
			asynchronous++
			if candidate.plan.Primary != coro.PrimaryCoroutine || candidate.plan.Demand != coro.AsyncDemand || !candidate.plan.Effect.MaySuspend() {
				t.Fatalf("coroutine candidate = %+v", candidate)
			}
		default:
			t.Fatalf("unexpected candidate emission: %+v", candidate)
		}
	}
	if plain != 1 || asynchronous != 1 {
		t.Fatalf("candidate classes: plain=%d coroutine=%d", plain, asynchronous)
	}
}

func TestResolveCoroInterfaceDispatchPlanPointerPromotedMethodEntry(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type Writer interface {
	Write([]byte) (int, error)
	Close() error
}
type PointerOnlyWriter struct{}
func (PointerOnlyWriter) Write(buffer []byte) (int, error) { <-gate; return len(buffer), nil }
func (*PointerOnlyWriter) Close() error { return nil }
func Keep() Writer { return &PointerOnlyWriter{} }
func Root(writer Writer) (int, error) { return writer.Write([]byte("payload")) }
`
	fixture := buildCoroPointerPromotedInterfaceDispatchFixture(t, source)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.candidates) != 1 {
		t.Fatalf("candidates = %d, want one pointer-promoted method: %+v", len(resolved.candidates), resolved.candidates)
	}
	candidate := resolved.candidates[0]
	dynamicPointer, dynamicIsPointer := types.Unalias(candidate.receiver).Underlying().(*types.Pointer)
	if !dynamicIsPointer || !types.Identical(dynamicPointer.Elem(), candidate.targetReceiver) {
		t.Fatalf("dynamic receiver %s does not promote declared receiver %s", candidate.receiver, candidate.targetReceiver)
	}
	if candidate.methodEntry == nil || candidate.methodEntry == candidate.function || candidate.methodEntry.Signature == nil ||
		candidate.methodEntry.Signature.Recv() == nil || !types.Identical(candidate.methodEntry.Signature.Recv().Type(), candidate.receiver) {
		t.Fatalf("pointer-promoted method entry = %v; target=%v dynamic receiver=%s", candidate.methodEntry, candidate.function, candidate.receiver)
	}
	if !strings.Contains(candidate.methodEntry.Synthetic, "wrapper") {
		t.Fatalf("method entry %s is not the exact pointer method-set wrapper: synthetic=%q", candidate.methodEntry, candidate.methodEntry.Synthetic)
	}
}

func TestResolveCoroInterfaceDispatchPlanFailsClosed(t *testing.T) {
	closed := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer closed.program.Dispose()
	open := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAOpen)
	defer open.program.Dispose()
	other := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer other.program.Dispose()

	tests := []struct {
		name string
		plan *coro.SSAPlan
		call *ssa.Call
		want string
	}{
		{name: "nil plan", call: closed.invoke, want: "exact call and compilation plan"},
		{name: "nil call", plan: closed.plan, want: "exact call and compilation plan"},
		{name: "open", plan: open.plan, call: open.invoke, want: "closed nonempty Dispatch CallPlan"},
		{name: "missing exact call plan", plan: closed.plan, call: other.invoke, want: "no exact compilation CallPlan"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveCoroInterfaceDispatchPlan(test.plan, test.call)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	resolved, err := resolveCoroInterfaceDispatchPlan(closed.plan, closed.invoke)
	if err != nil {
		t.Fatal(err)
	}
	target := resolved.candidates[0].function
	original := target.Signature
	recv := original.Recv()
	badParam := types.NewVar(0, target.Pkg.Pkg, "buffer", types.Typ[types.Int])
	target.Signature = types.NewSignatureType(recv, nil, nil, types.NewTuple(badParam), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("signature conflict error = %v", err)
	}

	originalFreeVars := target.FreeVars
	target.FreeVars = []*ssa.FreeVar{nil}
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, closed.invoke)
	target.FreeVars = originalFreeVars
	if err == nil || !strings.Contains(err.Error(), "captured or nested methods") {
		t.Fatalf("free-variable error = %v", err)
	}

	constraint := types.NewInterfaceType(nil, nil)
	constraint.Complete()
	typeParam := types.NewTypeParam(types.NewTypeName(0, target.Pkg.Pkg, "T", nil), constraint)
	named := types.NewNamed(types.NewTypeName(0, target.Pkg.Pkg, "GenericReceiver", nil), types.NewStruct(nil, nil), nil)
	named.SetTypeParams([]*types.TypeParam{typeParam})
	receiverTypeParam := types.NewTypeParam(types.NewTypeName(0, target.Pkg.Pkg, "T", nil), constraint)
	instantiated, instantiateErr := types.Instantiate(nil, named, []types.Type{receiverTypeParam}, false)
	if instantiateErr != nil {
		t.Fatal(instantiateErr)
	}
	genericRecv := types.NewVar(0, target.Pkg.Pkg, "writer", types.NewPointer(instantiated))
	target.Signature = types.NewSignatureType(genericRecv, []*types.TypeParam{receiverTypeParam}, nil, original.Params(), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "generic") {
		t.Fatalf("generic receiver error = %v", err)
	}

	badRecv := types.NewVar(0, target.Pkg.Pkg, "writer", types.Typ[types.Int])
	target.Signature = types.NewSignatureType(badRecv, nil, nil, original.Params(), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "does not implement invoke interface") {
		t.Fatalf("receiver conflict error = %v", err)
	}
}

func TestResolveCoroInterfaceDispatchPlanRejectsVariadicAndABIDirective(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "variadic",
			source: `package foo
type Writer interface { Write(...byte) int }
type Concrete struct{}
func (Concrete) Write(buffer ...byte) int { return len(buffer) }
func Root(writer Writer) int { return writer.Write(1, 2) }
`,
			want: "variadic method",
		},
		{
			name: "ABI directive",
			source: `package foo
import _ "unsafe"
type Writer interface { Write([]byte) int }
type Concrete struct{}
//go:linkname redirectedWrite example.com/redirectedWrite
func (Concrete) Write(buffer []byte) int { return len(buffer) }
func Root(writer Writer) int { return writer.Write(nil) }
`,
			want: "ABI directive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildCoroInterfaceDispatchFixture(t, test.source, coro.DynamicCHAClosed)
			defer fixture.program.Dispose()
			_, err := resolveCoroInterfaceDispatchPlan(fixture.plan, fixture.invoke)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

type coroInterfaceDispatchFixture struct {
	program llssa.Program
	plan    *coro.SSAPlan
	invoke  *ssa.Call
}

func buildCoroInterfaceDispatchFixture(t *testing.T, source string, resolution coro.DynamicResolution) coroInterfaceDispatchFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{EnableCoroChannel: true},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    resolution,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	return coroInterfaceDispatchFixture{program: program, plan: plan, invoke: invoke}
}

func buildCoroPointerPromotedInterfaceDispatchFixture(t *testing.T, source string) coroInterfaceDispatchFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{EnableCoroChannel: true},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	var declared, wrapper *ssa.Function
	for _, function := range universe.Functions() {
		if function == nil || function.Name() != "Write" || function.Signature == nil || function.Signature.Recv() == nil {
			continue
		}
		_, pointer := types.Unalias(function.Signature.Recv().Type()).Underlying().(*types.Pointer)
		switch {
		case !pointer && function.Synthetic == "":
			declared = function
		case pointer && strings.Contains(function.Synthetic, "wrapper"):
			wrapper = function
		}
	}
	if declared == nil || wrapper == nil {
		program.Dispose()
		t.Fatalf("pointer promotion fixture methods: declared=%v wrapper=%v", declared, wrapper)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
		ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
			if function == wrapper {
				return declared, true, nil
			}
			return function, true, nil
		},
	})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	return coroInterfaceDispatchFixture{program: program, plan: plan, invoke: invoke}
}

func coroInterfaceDispatchFindInvoke(t *testing.T, function *ssa.Function) *ssa.Call {
	t.Helper()
	if function == nil {
		t.Fatal("missing Root function")
	}
	var result *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || !call.Common().IsInvoke() {
				continue
			}
			if result != nil {
				t.Fatal("Root has more than one interface invoke")
			}
			result = call
		}
	}
	if result == nil {
		t.Fatal("Root has no interface invoke")
	}
	return result
}
