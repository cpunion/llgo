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

package ssa

import (
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

type coroDynamicDispatchTestFixture struct {
	prog       Program
	pkg        Package
	signature  *types.Signature
	result     Type
	hash       [16]byte
	plainEntry Function
	coroEntry  Function
	coroOnly   Expr
	dual       Expr
}

func TestCoroDynamicDispatchV1LLVM19CapturedCoroAndDualEntries(t *testing.T) {
	if llvmMajorVersion() != 19 {
		t.Skipf("dynamic coroutine descriptor IR proof is focused on LLVM 19, using %s", llvm.Version)
	}
	fixture := newCoroDynamicDispatchTestFixture(t)
	prog, pkg := fixture.prog, fixture.pkg

	for _, test := range []struct {
		name       string
		descriptor Expr
		flags      uint64
		plain      bool
	}{
		{"coro-only", fixture.coroOnly, uint64(CoroDispatchFlagHasCoro), false},
		{"plain+coro", fixture.dual, uint64(CoroDispatchFlagHasPlain | CoroDispatchFlagHasCoro), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initializer := test.descriptor.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 8 {
				t.Fatalf("descriptor is not the shared eight-field constant: %v", initializer)
			}
			if got := initializer.Operand(1).ZExtValue(); got != test.flags {
				t.Fatalf("flags = %#x, want %#x", got, test.flags)
			}
			plain := coroPlainDispatchFunction(initializer.Operand(4))
			if test.plain {
				if plain.IsNil() || plain.C != fixture.plainEntry.impl.C {
					t.Fatalf("plain entry = %v, want %s", plain, fixture.plainEntry.Name())
				}
			} else if initializer.Operand(4).IsAConstantPointerNull().IsNil() {
				t.Fatalf("coro-only descriptor has a plain entry: %v", initializer.Operand(4))
			}
			coro := coroPlainDispatchFunction(initializer.Operand(5))
			if coro.IsNil() || coro.C != fixture.coroEntry.impl.C {
				t.Fatalf("coro entry = %v, want %s", coro, fixture.coroEntry.Name())
			}
			if got, want := initializer.Operand(6).ZExtValue(), prog.SizeOf(fixture.result); got != want {
				t.Fatalf("result size = %d, want %d", got, want)
			}
			if got, want := initializer.Operand(7).ZExtValue(), prog.AlignOf(fixture.result); got != want {
				t.Fatalf("result align = %d, want %d", got, want)
			}
		})
	}

	// Identical frontend publication is idempotent and uses the same global.
	again := pkg.NewCoroDispatchDescriptor("dual_descriptor", fixture.descriptorOptions(
		CoroDispatchFlagHasPlain|CoroDispatchFlagHasCoro,
	))
	if again.impl.C != fixture.dual.impl.C {
		t.Fatal("identical dynamic descriptor materialization did not reuse the global")
	}

	ir := pkg.String()
	plainBody := coroPlainDispatchIRFunction(ir, fixture.plainEntry.Name())
	if !regexp.MustCompile(`define i32 @captured_plain_entry\(ptr [^,]+, i32 `).MatchString(plainBody) {
		t.Fatalf("plain entry does not have (env,args)->results ABI:\n%s", plainBody)
	}
	coroBody := coroPlainDispatchIRFunction(ir, fixture.coroEntry.Name())
	if !regexp.MustCompile(`define ptr @captured_coro_entry\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `).MatchString(coroBody) {
		t.Fatalf("coro entry does not have (g,out,env,args)->handle ABI:\n%s", coroBody)
	}
	producer := coroPlainDispatchIRFunction(ir, "captured_dispatch_value")
	if !strings.Contains(producer, "ret { ptr, ptr } { ptr @dual_descriptor, ptr @captured_env }") {
		t.Fatalf("captured value is not canonical {descriptor,nonnil-env}:\n%s", producer)
	}
	coroOnlyProducer := coroPlainDispatchIRFunction(ir, "captured_coro_only_value")
	if !strings.Contains(coroOnlyProducer, "ret { ptr, ptr } { ptr @coro_only_descriptor, ptr @captured_env }") {
		t.Fatalf("coro-only value is not canonical {descriptor,nonnil-env}:\n%s", coroOnlyProducer)
	}

	assertCoroDynamicDispatchGuards(t, ir, "dynamic_coro_call", true)
	assertCoroDynamicDispatchGuards(t, ir, "dynamic_plain_call", false)
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dynamic coroutine dispatch module: %v\n%s", err, ir)
	}
}

func TestCoroDynamicDispatchV1RejectsInvalidCapabilitiesAndEntries(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)

	options := fixture.descriptorOptions(0)
	coroPlainDispatchMustPanicContains(t, "require HasPlain or HasCoro", func() {
		fixture.pkg.NewCoroDispatchDescriptor("zero_flags", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro | 1<<31)
	coroPlainDispatchMustPanicContains(t, "unknown bits", func() {
		fixture.pkg.NewCoroDispatchDescriptor("unknown_flags", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.CoroEntry = Nil
	coroPlainDispatchMustPanicContains(t, "requires a coroutine entry", func() {
		fixture.pkg.NewCoroDispatchDescriptor("missing_coro", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.PlainEntry = fixture.plainEntry.Expr
	coroPlainDispatchMustPanicContains(t, "plain entry without its capability", func() {
		fixture.pkg.NewCoroDispatchDescriptor("stray_plain", options)
	})

	physical := fixture.prog.PhysicalFuncDecl(fixture.signature, InGo)
	badPlain := fixture.pkg.NewFunc("bad_plain_entry", physical, InC)
	options = fixture.descriptorOptions(CoroDispatchFlagHasPlain)
	options.PlainEntry = badPlain.Expr
	coroPlainDispatchMustPanicContains(t, "plain entry does not match", func() {
		fixture.pkg.NewCoroDispatchDescriptor("bad_plain_signature", options)
	})

	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.Result = nil
	coroPlainDispatchMustPanicContains(t, "result layout", func() {
		fixture.pkg.NewCoroDispatchDescriptor("missing_layout", options)
	})
}

func newCoroDynamicDispatchTestFixture(t *testing.T) *coroDynamicDispatchTestFixture {
	t.Helper()
	Initialize(InitAll)
	prog := NewProgram(nil)
	installCoroPlainDispatchTestRuntime(prog)
	pkg := prog.NewPackage("corodynamicdispatch", "coro/dynamic/dispatch")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	signature := coroPlainDispatchTestSignature(
		[]types.Type{types.Typ[types.Uint32]},
		[]types.Type{types.Typ[types.Uint32]},
	)
	result := prog.Struct(prog.Uint32())
	hash := [16]byte{
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28,
	}
	plainEntry := pkg.NewFunc("captured_plain_entry", prog.CoroDispatchPlainEntrySignature(signature), InC)
	pb := plainEntry.MakeBody(1)
	pb.Return(plainEntry.Param(1))
	pb.EndBuild()
	pb.Dispose()
	coroEntry := pkg.NewFunc("captured_coro_entry", prog.CoroDispatchCoroEntrySignature(signature), InC)
	cb := coroEntry.MakeBody(1)
	cb.Return(coroEntry.Param(2))
	cb.EndBuild()
	cb.Dispose()

	fixture := &coroDynamicDispatchTestFixture{
		prog: prog, pkg: pkg, signature: signature, result: result, hash: hash,
		plainEntry: plainEntry, coroEntry: coroEntry,
	}
	fixture.coroOnly = pkg.NewCoroDispatchDescriptor(
		"coro_only_descriptor", fixture.descriptorOptions(CoroDispatchFlagHasCoro),
	)
	fixture.dual = pkg.NewCoroDispatchDescriptor(
		"dual_descriptor",
		fixture.descriptorOptions(CoroDispatchFlagHasPlain|CoroDispatchFlagHasCoro),
	)

	env := pkg.NewVarEx("captured_env", prog.Pointer(prog.Uint32()))
	env.Init(prog.IntVal(7, prog.Uint32()))
	makeValue := func(name string, descriptor Expr) {
		producerSig := coroPlainDispatchTestSignature(nil, []types.Type{signature})
		producer := pkg.NewFunc(name, producerSig, InGo)
		b := producer.MakeBody(1)
		b.Return(b.MakeCoroDispatchValue(signature, descriptor, env.Expr))
		b.EndBuild()
		b.Dispose()
	}
	makeValue("captured_dispatch_value", fixture.dual)
	makeValue("captured_coro_only_value", fixture.coroOnly)

	callOptions := CoroDispatchCallOptions{
		Version: CoroDispatchVersionV1,
		ABIHash: hash,
		Result:  result,
	}
	coroCallerSig := coroPlainDispatchTestSignature(
		[]types.Type{
			signature,
			types.Typ[types.UnsafePointer],
			types.Typ[types.UnsafePointer],
			types.Typ[types.Uint32],
		},
		[]types.Type{types.Typ[types.UnsafePointer]},
	)
	coroCaller := pkg.NewFunc("dynamic_coro_call", coroCallerSig, InGo)
	ccb := coroCaller.MakeBody(1)
	handle := ccb.CallCoroDispatchCoro(
		coroCaller.Param(0), coroCaller.Param(1), coroCaller.Param(2),
		[]Expr{coroCaller.Param(3)}, callOptions,
	)
	ccb.Return(handle)
	ccb.EndBuild()
	ccb.Dispose()

	plainCallerSig := coroPlainDispatchTestSignature(
		[]types.Type{signature, types.Typ[types.Uint32]},
		[]types.Type{types.Typ[types.Uint32]},
	)
	plainCaller := pkg.NewFunc("dynamic_plain_call", plainCallerSig, InGo)
	pcb := plainCaller.MakeBody(1)
	value := pcb.CallCoroDispatchPlain(
		plainCaller.Param(0), []Expr{plainCaller.Param(1)}, callOptions,
	)
	pcb.Return(value)
	pcb.EndBuild()
	pcb.Dispose()
	return fixture
}

func (f *coroDynamicDispatchTestFixture) descriptorOptions(flags uint32) CoroDispatchDescriptorOptions {
	options := CoroDispatchDescriptorOptions{
		Version:   CoroDispatchVersionV1,
		Flags:     flags,
		ABIHash:   f.hash,
		Signature: f.signature,
		Result:    f.result,
	}
	if flags&CoroDispatchFlagHasPlain != 0 {
		options.PlainEntry = f.plainEntry.Expr
	}
	if flags&CoroDispatchFlagHasCoro != 0 {
		options.CoroEntry = f.coroEntry.Expr
	}
	return options
}

func assertCoroDynamicDispatchGuards(t *testing.T, ir, name string, coro bool) {
	t.Helper()
	body := coroPlainDispatchIRFunction(ir, name)
	if body == "" {
		t.Fatalf("missing dynamic dispatch caller %q:\n%s", name, ir)
	}
	for _, guard := range []string{
		"coro.dispatch.version.invalid",
		"coro.dispatch.flags.unknown",
		"coro.dispatch.flags.empty",
		"coro.dispatch.capability.missing",
		"coro.dispatch.plain.entry.mismatch",
		"coro.dispatch.coro.entry.mismatch",
		"coro.dispatch.nocapture.env.nonnull",
		"coro.dispatch.hash.lo.invalid",
		"coro.dispatch.hash.hi.invalid",
		"coro.dispatch.result.size.invalid",
		"coro.dispatch.result.align.invalid",
	} {
		if !strings.Contains(body, guard) {
			t.Fatalf("dynamic caller %q lacks fail-closed guard %q:\n%s", name, guard, body)
		}
	}
	if got := strings.Count(body, "call void @llvm.trap()"); got != 1 {
		t.Fatalf("dynamic caller %q trap sites = %d, want one combined descriptor trap:\n%s", name, got, body)
	}
	assertCall := strings.Index(body, "AssertNilDeref")
	descriptorLoad := strings.Index(body, "load { i32, i32, i64, i64, ptr, ptr")
	guardBranch := strings.LastIndex(body, "br i1 %coro.dispatch.invalid")
	if assertCall < 0 || descriptorLoad < 0 || guardBranch < 0 ||
		assertCall > descriptorLoad || descriptorLoad > guardBranch {
		t.Fatalf("dynamic caller %q does not validate nil then descriptor before dispatch:\n%s", name, body)
	}
	if coro {
		if !regexp.MustCompile(`call ptr %[^(]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `).MatchString(body) {
			t.Fatalf("dynamic coro caller has no typed (g,out,env,args)->handle call:\n%s", body)
		}
		return
	}
	if !regexp.MustCompile(`call i32 %[^(]+\(ptr [^,]+, i32 `).MatchString(body) {
		t.Fatalf("dynamic plain caller has no typed (env,args)->results call:\n%s", body)
	}
}
