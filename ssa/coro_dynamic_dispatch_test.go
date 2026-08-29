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
	prog         Program
	pkg          Package
	signature    *types.Signature
	result       Type
	hash         [16]byte
	plainEntry   Function
	outcomeEntry Function
	coroEntry    Function
	outcomeOnly  Expr
	coroOnly     Expr
	dual         Expr
}

func TestCoroDynamicDispatchV2LLVM22CapturedStructuredEntries(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)
	prog, pkg := fixture.prog, fixture.pkg

	for _, test := range []struct {
		name       string
		descriptor Expr
		flags      uint64
		plain      bool
		structured Function
		code       Function
	}{
		{"outcome-only", fixture.outcomeOnly, uint64(CoroDispatchFlagHasOutcome), false, fixture.outcomeEntry, fixture.outcomeEntry},
		{"coro-only", fixture.coroOnly, uint64(CoroDispatchFlagHasCoro), false, fixture.coroEntry, fixture.coroEntry},
		{"plain+coro", fixture.dual, uint64(CoroDispatchFlagHasPlain | CoroDispatchFlagHasCoro), true, fixture.coroEntry, fixture.coroEntry},
	} {
		t.Run(test.name, func(t *testing.T) {
			initializer := test.descriptor.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 9 {
				t.Fatalf("descriptor is not the shared nine-field constant: %v", initializer)
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
			structured := coroPlainDispatchFunction(initializer.Operand(5))
			if structured.IsNil() || structured.C != test.structured.impl.C {
				t.Fatalf("structured entry = %v, want %s", structured, test.structured.Name())
			}
			if got, want := initializer.Operand(6).ZExtValue(), prog.SizeOf(fixture.result); got != want {
				t.Fatalf("result size = %d, want %d", got, want)
			}
			if got, want := initializer.Operand(7).ZExtValue(), prog.AlignOf(fixture.result); got != want {
				t.Fatalf("result align = %d, want %d", got, want)
			}
			code := coroPlainDispatchFunction(initializer.Operand(8))
			if code.IsNil() || code.C != test.code.impl.C {
				t.Fatalf("code identity = %v, want %s", code, test.code.Name())
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
	outcomeBody := coroPlainDispatchIRFunction(ir, fixture.outcomeEntry.Name())
	if !regexp.MustCompile(`define void @captured_outcome_entry\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `).MatchString(outcomeBody) {
		t.Fatalf("outcome entry does not have (g,out,completion,env,args)->void ABI:\n%s", outcomeBody)
	}
	producer := coroPlainDispatchIRFunction(ir, "captured_dispatch_value")
	if !strings.Contains(producer, "ret { ptr, ptr } { ptr @dual_descriptor, ptr @captured_env }") {
		t.Fatalf("captured value is not canonical {descriptor,nonnil-env}:\n%s", producer)
	}
	coroOnlyProducer := coroPlainDispatchIRFunction(ir, "captured_coro_only_value")
	if !strings.Contains(coroOnlyProducer, "ret { ptr, ptr } { ptr @coro_only_descriptor, ptr @captured_env }") {
		t.Fatalf("coro-only value is not canonical {descriptor,nonnil-env}:\n%s", coroOnlyProducer)
	}

	assertCoroDynamicDispatchGuards(t, ir, "dynamic_coro_call", "coro")
	assertCoroDynamicDispatchGuards(t, ir, "dynamic_outcome_call", "outcome")
	assertCoroDynamicDispatchGuards(t, ir, "dynamic_plain_call", "plain")
	assertCoroDynamicDispatchProbeGuards(t, ir, "dynamic_has_coro")
	assertCoroDynamicDispatchProbeGuards(t, ir, "dynamic_has_coro_and_code")
	assertCoroDynamicDispatchProbeGuards(t, ir, "dynamic_capabilities_and_code")
	pairProbe := coroPlainDispatchIRFunction(ir, "dynamic_has_coro_and_code")
	if !regexp.MustCompile(`extractvalue \{ i32, i32, i64, i64, ptr, ptr, i64, i64, ptr \} [^,]+, 8`).MatchString(pairProbe) ||
		!strings.Contains(pairProbe, "ret { i1, ptr }") {
		t.Fatalf("dynamic capability/code probe does not return the compiler-injected code identity:\n%s", pairProbe)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dynamic coroutine dispatch module: %v\n%s", err, ir)
	}
}

func TestCoroDynamicDispatchV2RejectsInvalidCapabilitiesAndEntries(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)

	options := fixture.descriptorOptions(0)
	coroPlainDispatchMustPanicContains(t, "require HasPlain, HasOutcome, or HasCoro", func() {
		fixture.pkg.NewCoroDispatchDescriptor("zero_flags", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro | 1<<31)
	coroPlainDispatchMustPanicContains(t, "unknown bits", func() {
		fixture.pkg.NewCoroDispatchDescriptor("unknown_flags", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro | CoroDispatchFlagRuntimeTyped)
	coroPlainDispatchMustPanicContains(t, "cannot use RuntimeTyped", func() {
		fixture.pkg.NewCoroDispatchDescriptor("runtime_typed_compiler_descriptor", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.CoroEntry = Nil
	coroPlainDispatchMustPanicContains(t, "requires a coroutine entry", func() {
		fixture.pkg.NewCoroDispatchDescriptor("missing_coro", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasOutcome)
	options.OutcomeEntry = Nil
	coroPlainDispatchMustPanicContains(t, "requires an outcome entry", func() {
		fixture.pkg.NewCoroDispatchDescriptor("missing_outcome", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasOutcome | CoroDispatchFlagHasCoro)
	coroPlainDispatchMustPanicContains(t, "mutually exclusive", func() {
		fixture.pkg.NewCoroDispatchDescriptor("outcome_and_coro", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.PlainEntry = fixture.plainEntry.Expr
	coroPlainDispatchMustPanicContains(t, "plain entry without its capability", func() {
		fixture.pkg.NewCoroDispatchDescriptor("stray_plain", options)
	})

	physical := fixture.prog.PhysicalFuncDecl(fixture.signature, InGo)
	badPlain := fixture.pkg.NewFunc("bad_plain_entry", physical, InC)
	options = fixture.descriptorOptions(CoroDispatchFlagHasPlain | CoroDispatchFlagPlainNoUnwind)
	options.PlainEntry = badPlain.Expr
	coroPlainDispatchMustPanicContains(t, "plain entry does not match", func() {
		fixture.pkg.NewCoroDispatchDescriptor("bad_plain_signature", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasPlain)
	coroPlainDispatchMustPanicContains(t, "plain-only capability requires PlainNoUnwind", func() {
		fixture.pkg.NewCoroDispatchDescriptor("plain_without_no_unwind", options)
	})
	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro | CoroDispatchFlagPlainNoUnwind)
	coroPlainDispatchMustPanicContains(t, "PlainNoUnwind requires HasPlain", func() {
		fixture.pkg.NewCoroDispatchDescriptor("no_unwind_without_plain", options)
	})

	options = fixture.descriptorOptions(CoroDispatchFlagHasCoro)
	options.Result = nil
	coroPlainDispatchMustPanicContains(t, "result layout", func() {
		fixture.pkg.NewCoroDispatchDescriptor("missing_layout", options)
	})
}

func TestCoroDispatchDescriptorNonNilSuppressesImplicitLoadNilCheck(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)
	zero := fixture.prog.Zero(fixture.prog.Closure(fixture.signature))

	probe := fixture.pkg.NewFunc(
		"guarded_dynamic_probe",
		coroPlainDispatchTestSignature(nil, []types.Type{types.Typ[types.Bool]}),
		InGo,
	)
	probeBuilder := probe.MakeBody(1)
	probeBuilder.Return(probeBuilder.CoroDispatchHasCoro(zero, CoroDispatchCallOptions{
		Version:          CoroDispatchVersionV2,
		ABIHash:          fixture.hash,
		Result:           fixture.result,
		DescriptorNonNil: true,
	}))
	probeBuilder.EndBuild()
	probeBuilder.Dispose()

	plain := fixture.pkg.NewFunc(
		"guarded_plain_call",
		coroPlainDispatchTestSignature(nil, []types.Type{types.Typ[types.Uint32]}),
		InGo,
	)
	plainBuilder := plain.MakeBody(1)
	plainBuilder.Return(plainBuilder.CallCoroPlainDispatch(
		zero,
		[]Expr{fixture.prog.IntVal(1, fixture.prog.Uint32())},
		CoroPlainDispatchCallOptions{
			Version:          CoroPlainDispatchVersionV2,
			Flags:            CoroPlainDispatchFlagsV2,
			ABIHash:          fixture.hash,
			Result:           fixture.result,
			DescriptorNonNil: true,
		},
	))
	plainBuilder.EndBuild()
	plainBuilder.Dispose()

	ir := fixture.pkg.String()
	for _, name := range []string{"guarded_dynamic_probe", "guarded_plain_call"} {
		body := coroPlainDispatchIRFunction(ir, name)
		if body == "" {
			t.Fatalf("missing DescriptorNonNil fixture %q:\n%s", name, ir)
		}
		if strings.Contains(body, "AssertNilDeref") {
			t.Fatalf("DescriptorNonNil fixture %q recreated an implicit nil helper:\n%s", name, body)
		}
		if !strings.Contains(body, "load { i32, i32, i64, i64, ptr, ptr") {
			t.Fatalf("DescriptorNonNil fixture %q omitted descriptor validation:\n%s", name, body)
		}
	}
	if err := llvm.VerifyModule(fixture.pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify DescriptorNonNil module: %v\n%s", err, ir)
	}
}

func TestCoroDispatchTrustedDescriptorSkipsRepeatedContractValidation(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)
	callOptions := CoroDispatchCallOptions{
		Version:           CoroDispatchVersionV2,
		ABIHash:           fixture.hash,
		Result:            fixture.result,
		TrustedDescriptor: true,
	}

	plainSignature := coroPlainDispatchTestSignature(
		[]types.Type{fixture.signature, types.Typ[types.Uint32]},
		[]types.Type{types.Typ[types.Uint32]},
	)
	plain := fixture.pkg.NewFunc("trusted_dynamic_plain_call", plainSignature, InGo)
	plainBuilder := plain.MakeBody(1)
	plainBuilder.Return(plainBuilder.CallCoroDispatchPlain(
		plain.Param(0), []Expr{plain.Param(1)}, callOptions,
	))
	plainBuilder.EndBuild()
	plainBuilder.Dispose()

	probeSignature := coroPlainDispatchTestSignature(
		[]types.Type{fixture.signature},
		[]types.Type{types.Typ[types.Bool]},
	)
	probe := fixture.pkg.NewFunc("trusted_dynamic_has_coro", probeSignature, InGo)
	probeBuilder := probe.MakeBody(1)
	probeBuilder.Return(probeBuilder.CoroDispatchHasCoro(probe.Param(0), callOptions))
	probeBuilder.EndBuild()
	probeBuilder.Dispose()

	ir := fixture.pkg.String()
	for _, name := range []string{"trusted_dynamic_plain_call", "trusted_dynamic_has_coro"} {
		body := coroPlainDispatchIRFunction(ir, name)
		if body == "" {
			t.Fatalf("missing trusted descriptor fixture %q:\n%s", name, ir)
		}
		for _, forbidden := range []string{
			"coro.dispatch.version.invalid",
			"coro.dispatch.flags.unknown",
			"coro.dispatch.hash.invalid",
			"coro.dispatch.result.size.invalid",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("trusted descriptor fixture %q retained %q:\n%s", name, forbidden, body)
			}
		}
	}
	if err := llvm.VerifyModule(fixture.pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify trusted descriptor module: %v\n%s", err, ir)
	}
}

func TestCoroDispatchStructuredOnlySelectionLoadsOnlyRequiredWords(t *testing.T) {
	fixture := newCoroDynamicDispatchTestFixture(t)
	options := CoroDispatchCallOptions{
		Version:           CoroDispatchVersionV2,
		ABIHash:           fixture.hash,
		Result:            fixture.result,
		DescriptorNonNil:  true,
		TrustedDescriptor: true,
	}
	params := []types.Type{
		fixture.signature,
		types.Typ[types.UnsafePointer],
		types.Typ[types.UnsafePointer],
		types.Typ[types.UnsafePointer],
		types.Typ[types.Uint32],
	}
	makeCaller := func(name string, needCode bool) {
		var results []types.Type
		if needCode {
			results = []types.Type{types.Typ[types.UnsafePointer]}
		}
		fn := fixture.pkg.NewFunc(name, coroPlainDispatchTestSignature(params, results), InGo)
		b := fn.MakeBody(1)
		selection := b.PrepareCoroDispatchStructuredOnly(fn.Param(0), options, needCode)
		b.CallPreparedCoroDispatchOutcomeOnly(
			selection,
			fn.Param(1), fn.Param(2), fn.Param(3),
			[]Expr{fn.Param(4)},
		)
		if needCode {
			b.Return(selection.CodeEntry())
		} else {
			b.Return()
		}
		b.EndBuild()
		b.Dispose()
	}
	makeCaller("outcome_only_narrow_call", false)
	makeCaller("outcome_only_narrow_call_with_code", true)
	coroCaller := fixture.pkg.NewFunc(
		"coroutine_only_narrow_call",
		coroPlainDispatchTestSignature(
			[]types.Type{
				fixture.signature,
				types.Typ[types.UnsafePointer],
				types.Typ[types.UnsafePointer],
				types.Typ[types.Uint32],
			},
			[]types.Type{types.Typ[types.UnsafePointer]},
		),
		InGo,
	)
	coroBuilder := coroCaller.MakeBody(1)
	coroSelection := coroBuilder.PrepareCoroDispatchStructuredOnly(coroCaller.Param(0), options, false)
	handle := coroBuilder.CallPreparedCoroDispatchCoroOnly(
		coroSelection,
		coroCaller.Param(1), coroCaller.Param(2),
		[]Expr{coroCaller.Param(3)},
	)
	coroBuilder.Return(handle)
	coroBuilder.EndBuild()
	coroBuilder.Dispose()

	ir := fixture.pkg.String()
	for _, test := range []struct {
		name     string
		wantCode bool
		coro     bool
	}{
		{name: "outcome_only_narrow_call"},
		{name: "outcome_only_narrow_call_with_code", wantCode: true},
		{name: "coroutine_only_narrow_call", coro: true},
	} {
		body := coroPlainDispatchIRFunction(ir, test.name)
		if body == "" {
			t.Fatalf("missing narrow structured-only caller %q:\n%s", test.name, ir)
		}
		for _, forbidden := range []string{
			"load { i32, i32, i64, i64, ptr, ptr",
			"coro.dispatch.flags",
			"coro.dispatch.version",
			"coro.dispatch.hash",
			" and i32 ",
			"AssertNilDeref",
			", i32 0, i32 4",
			", i32 0, i32 6",
			", i32 0, i32 7",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("narrow structured-only caller %q retained %q:\n%s", test.name, forbidden, body)
			}
		}
		if !strings.Contains(body, ", i32 0, i32 5") {
			t.Fatalf("narrow structured-only caller %q did not load the structured entry word:\n%s", test.name, body)
		}
		gotCode := strings.Contains(body, ", i32 0, i32 8")
		if gotCode != test.wantCode {
			t.Fatalf("narrow structured-only caller %q code-entry load = %t, want %t:\n%s", test.name, gotCode, test.wantCode, body)
		}
		fields := regexp.MustCompile(`getelementptr[^\n]*, i32 0, i32 ([0-9]+)`).FindAllStringSubmatch(body, -1)
		wantFields := []string{"5"}
		if test.wantCode {
			wantFields = append(wantFields, "8")
		}
		if len(fields) != len(wantFields) {
			t.Fatalf("narrow structured-only caller %q descriptor field loads = %v, want %v:\n%s", test.name, fields, wantFields, body)
		}
		for index, field := range fields {
			if len(field) != 2 || field[1] != wantFields[index] {
				t.Fatalf("narrow structured-only caller %q descriptor field load %d = %v, want %s:\n%s", test.name, index, field, wantFields[index], body)
			}
		}
		callPattern := `call void %[^(]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `
		if test.coro {
			callPattern = `call ptr %[^(]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `
		}
		if !regexp.MustCompile(callPattern).MatchString(body) {
			t.Fatalf("narrow structured-only caller %q has no typed call:\n%s", test.name, body)
		}
	}
	if err := llvm.VerifyModule(fixture.pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify narrow outcome-only module: %v\n%s", err, ir)
	}
}

func TestCoroDynamicDispatchAcceptsPackedVariadicSignature(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	installCoroPlainDispatchTestRuntime(prog)
	runtimePkg := prog.runtime()
	sliceName := types.NewTypeName(0, runtimePkg, "Slice", nil)
	types.NewNamed(sliceName, types.NewStruct(
		[]*types.Var{
			types.NewField(0, runtimePkg, "data", types.Typ[types.UnsafePointer], false),
			types.NewField(0, runtimePkg, "len", types.Typ[types.Int], false),
			types.NewField(0, runtimePkg, "cap", types.Typ[types.Int], false),
		},
		nil,
	), nil)
	runtimePkg.Scope().Insert(sliceName)
	pkg := prog.NewPackage("corodynamicvariadic", "coro/dynamic/variadic")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	valuesType := types.NewSlice(types.Typ[types.Int])
	signature := types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(types.NewParam(0, nil, "values", valuesType)),
		types.NewTuple(types.NewParam(0, nil, "", types.Typ[types.Int])),
		true,
	)
	result := prog.Struct(prog.Int())
	hash := [16]byte{1, 3, 3, 7}
	entry := pkg.NewFunc("variadic_plain_entry", prog.CoroDispatchPlainEntrySignature(signature), InC)
	entryBuilder := entry.MakeBody(1)
	entryBuilder.Return(prog.IntVal(7, prog.Int()))
	entryBuilder.EndBuild()
	entryBuilder.Dispose()

	descriptor := pkg.NewCoroDispatchDescriptor("variadic_descriptor", CoroDispatchDescriptorOptions{
		Version:    CoroDispatchVersionV2,
		Flags:      CoroDispatchFlagHasPlain | CoroDispatchFlagPlainNoUnwind,
		ABIHash:    hash,
		Signature:  signature,
		PlainEntry: entry.Expr,
		CodeEntry:  entry.Expr,
		Result:     result,
	})
	callerSignature := types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(
			types.NewParam(0, nil, "fn", signature),
			types.NewParam(0, nil, "values", valuesType),
		),
		types.NewTuple(types.NewParam(0, nil, "", types.Typ[types.Int])),
		false,
	)
	caller := pkg.NewFunc("variadic_dispatch_call", callerSignature, InGo)
	b := caller.MakeBody(1)
	fn := b.MakeCoroDispatchValue(signature, descriptor, Nil)
	value := b.CallCoroDispatchPlain(fn, []Expr{caller.Param(1)}, CoroDispatchCallOptions{
		Version:          CoroDispatchVersionV2,
		ABIHash:          hash,
		Result:           result,
		DescriptorNonNil: true,
	})
	b.Return(value)
	b.EndBuild()
	b.Dispose()

	ir := pkg.String()
	body := coroPlainDispatchIRFunction(ir, "variadic_dispatch_call")
	if !regexp.MustCompile(`call i64 %[^(]+\(ptr null, %"[^"]+\.Slice" %1\)`).MatchString(body) {
		t.Fatalf("variadic descriptor call did not use one fixed packed-slice argument:\n%s", body)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify packed variadic descriptor module: %v\n%s", err, ir)
	}
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
	outcomeEntry := pkg.NewFunc("captured_outcome_entry", prog.CoroDispatchOutcomeEntrySignature(signature), InC)
	ob := outcomeEntry.MakeBody(1)
	ob.Return()
	ob.EndBuild()
	ob.Dispose()

	fixture := &coroDynamicDispatchTestFixture{
		prog: prog, pkg: pkg, signature: signature, result: result, hash: hash,
		plainEntry: plainEntry, outcomeEntry: outcomeEntry, coroEntry: coroEntry,
	}
	fixture.outcomeOnly = pkg.NewCoroDispatchDescriptor(
		"outcome_only_descriptor", fixture.descriptorOptions(CoroDispatchFlagHasOutcome),
	)
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
	makeValue("captured_outcome_only_value", fixture.outcomeOnly)
	makeValue("captured_coro_only_value", fixture.coroOnly)

	callOptions := CoroDispatchCallOptions{
		Version: CoroDispatchVersionV2,
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

	outcomeCallerSig := coroPlainDispatchTestSignature(
		[]types.Type{
			signature,
			types.Typ[types.UnsafePointer],
			types.Typ[types.UnsafePointer],
			types.Typ[types.UnsafePointer],
			types.Typ[types.Uint32],
		},
		nil,
	)
	outcomeCaller := pkg.NewFunc("dynamic_outcome_call", outcomeCallerSig, InGo)
	ob = outcomeCaller.MakeBody(1)
	ob.CallCoroDispatchOutcome(
		outcomeCaller.Param(0), outcomeCaller.Param(1), outcomeCaller.Param(2),
		outcomeCaller.Param(3), []Expr{outcomeCaller.Param(4)}, callOptions,
	)
	ob.Return()
	ob.EndBuild()
	ob.Dispose()

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

	probeCallerSig := coroPlainDispatchTestSignature(
		[]types.Type{signature},
		[]types.Type{types.Typ[types.Bool]},
	)
	probeCaller := pkg.NewFunc("dynamic_has_coro", probeCallerSig, InGo)
	probeBuilder := probeCaller.MakeBody(1)
	probeBuilder.Return(probeBuilder.CoroDispatchHasCoro(probeCaller.Param(0), callOptions))
	probeBuilder.EndBuild()
	probeBuilder.Dispose()

	pairProbeSig := coroPlainDispatchTestSignature(
		[]types.Type{signature},
		[]types.Type{types.Typ[types.Bool], types.Typ[types.UnsafePointer]},
	)
	pairProbe := pkg.NewFunc("dynamic_has_coro_and_code", pairProbeSig, InGo)
	pairProbeBuilder := pairProbe.MakeBody(1)
	hasCoro, codeEntry := pairProbeBuilder.CoroDispatchHasCoroAndCodeEntry(pairProbe.Param(0), callOptions)
	pairProbeBuilder.Return(hasCoro, codeEntry)
	pairProbeBuilder.EndBuild()
	pairProbeBuilder.Dispose()

	tripleProbeSig := coroPlainDispatchTestSignature(
		[]types.Type{signature},
		[]types.Type{types.Typ[types.Bool], types.Typ[types.Bool], types.Typ[types.UnsafePointer]},
	)
	tripleProbe := pkg.NewFunc("dynamic_capabilities_and_code", tripleProbeSig, InGo)
	tripleProbeBuilder := tripleProbe.MakeBody(1)
	hasOutcome, hasCoro, codeEntry := tripleProbeBuilder.CoroDispatchCapabilitiesAndCodeEntry(
		tripleProbe.Param(0), callOptions,
	)
	tripleProbeBuilder.Return(hasOutcome, hasCoro, codeEntry)
	tripleProbeBuilder.EndBuild()
	tripleProbeBuilder.Dispose()
	return fixture
}

func (f *coroDynamicDispatchTestFixture) descriptorOptions(flags uint32) CoroDispatchDescriptorOptions {
	options := CoroDispatchDescriptorOptions{
		Version:   CoroDispatchVersionV2,
		Flags:     flags,
		ABIHash:   f.hash,
		Signature: f.signature,
		Result:    f.result,
	}
	if flags&CoroDispatchFlagHasPlain != 0 {
		options.PlainEntry = f.plainEntry.Expr
		options.CodeEntry = f.plainEntry.Expr
	}
	if flags&CoroDispatchFlagHasCoro != 0 {
		options.CoroEntry = f.coroEntry.Expr
		options.CodeEntry = f.coroEntry.Expr
	}
	if flags&CoroDispatchFlagHasOutcome != 0 {
		options.OutcomeEntry = f.outcomeEntry.Expr
		options.CodeEntry = f.outcomeEntry.Expr
	}
	return options
}

func assertCoroDynamicDispatchGuards(t *testing.T, ir, name, capability string) {
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
		"coro.dispatch.structured.entry.mismatch",
		"coro.dispatch.structured.flags.conflict",
		"coro.dispatch.plain.no-unwind-without-plain",
		"coro.dispatch.plain-only.no-unwind-missing",
		"coro.dispatch.nocapture.env.nonnull",
		"coro.dispatch.hash.invalid",
		"coro.dispatch.runtime-type.invalid",
		"coro.dispatch.result.size.invalid",
		"coro.dispatch.result.align.invalid",
		"coro.dispatch.code.nil",
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
	switch capability {
	case "coro":
		if !regexp.MustCompile(`call ptr %[^(]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `).MatchString(body) {
			t.Fatalf("dynamic coro caller has no typed (g,out,env,args)->handle call:\n%s", body)
		}
		return
	case "outcome":
		if !regexp.MustCompile(`call void %[^(]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 `).MatchString(body) {
			t.Fatalf("dynamic outcome caller has no typed (g,out,completion,env,args)->void call:\n%s", body)
		}
		return
	case "plain":
	default:
		t.Fatalf("unknown capability %q", capability)
	}
	if !regexp.MustCompile(`call i32 %[^(]+\(ptr [^,]+, i32 `).MatchString(body) {
		t.Fatalf("dynamic plain caller has no typed (env,args)->results call:\n%s", body)
	}
}

func assertCoroDynamicDispatchProbeGuards(t *testing.T, ir, name string) {
	t.Helper()
	body := coroPlainDispatchIRFunction(ir, name)
	if body == "" {
		t.Fatalf("missing dynamic dispatch capability probe %q:\n%s", name, ir)
	}
	for _, guard := range []string{
		"coro.dispatch.version.invalid",
		"coro.dispatch.flags.unknown",
		"coro.dispatch.flags.empty",
		"coro.dispatch.plain.entry.mismatch",
		"coro.dispatch.structured.entry.mismatch",
		"coro.dispatch.structured.flags.conflict",
		"coro.dispatch.nocapture.env.nonnull",
		"coro.dispatch.hash.invalid",
		"coro.dispatch.runtime-type.invalid",
		"coro.dispatch.result.size.invalid",
		"coro.dispatch.result.align.invalid",
	} {
		if !strings.Contains(body, guard) {
			t.Fatalf("dynamic capability probe %q lacks fail-closed guard %q:\n%s", name, guard, body)
		}
	}
	if strings.Contains(body, "coro.dispatch.capability.missing") {
		t.Fatalf("dynamic capability probe %q requires a specific capability:\n%s", name, body)
	}
	if got := strings.Count(body, "call void @llvm.trap()"); got != 1 {
		t.Fatalf("dynamic capability probe %q trap sites = %d, want one combined descriptor trap:\n%s", name, got, body)
	}
	assertCall := strings.Index(body, "AssertNilDeref")
	descriptorLoad := strings.Index(body, "load { i32, i32, i64, i64, ptr, ptr")
	guardBranch := strings.LastIndex(body, "br i1 %coro.dispatch.invalid")
	if assertCall < 0 || descriptorLoad < 0 || guardBranch < 0 ||
		assertCall > descriptorLoad || descriptorLoad > guardBranch {
		t.Fatalf("dynamic capability probe %q does not validate nil then descriptor before probing:\n%s", name, body)
	}
	returnsCapability := regexp.MustCompile(`ret i1 %[^ ]+`).MatchString(body) ||
		regexp.MustCompile(`insertvalue \{ i1(?:, i1)?, ptr \} [^,]+, i1 %[^,]+, 0`).MatchString(body)
	if !regexp.MustCompile(`and i32 [^,]+, 4`).MatchString(body) || !returnsCapability {
		t.Fatalf("dynamic capability probe %q does not return the HasCoro flag:\n%s", name, body)
	}
}
