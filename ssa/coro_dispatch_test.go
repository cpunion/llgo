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
	"fmt"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

type coroPlainDispatchTestFixture struct {
	prog        Program
	pkg         Package
	signature   *types.Signature
	result      Type
	hash        [16]byte
	descriptor  Expr
	descriptor2 Expr
	thunkName   string
	thunkName2  string
}

func TestCoroPlainDispatchV1TargetLayoutAndLowering(t *testing.T) {
	Initialize(InitAll)
	tests := []struct {
		name              string
		target            *Target
		pointerSize       int
		descriptorSize    uint64
		coroEntryOffset   uint64
		resultSizeOffset  uint64
		resultAlignOffset uint64
	}{
		{
			name:              "native64",
			pointerSize:       8,
			descriptorSize:    56,
			coroEntryOffset:   32,
			resultSizeOffset:  40,
			resultAlignOffset: 48,
		},
		{
			name:              "wasm32",
			target:            &Target{GOOS: "wasip1", GOARCH: "wasm"},
			pointerSize:       4,
			descriptorSize:    40,
			coroEntryOffset:   28,
			resultSizeOffset:  32,
			resultAlignOffset: 36,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoroPlainDispatchTestFixture(t, test.target)
			prog, pkg := fixture.prog, fixture.pkg

			if got := prog.PointerSize(); got != test.pointerSize {
				t.Fatalf("pointer size = %d, want %d", got, test.pointerSize)
			}
			closureType := prog.Closure(fixture.signature)
			if got, want := prog.SizeOf(closureType), uint64(test.pointerSize*2); got != want {
				t.Fatalf("dispatch value size = %d, want two pointers (%d)", got, want)
			}

			descriptor := fixture.descriptor
			if descriptor.kind != vkPtr || !descriptor.impl.IsGlobalConstant() {
				t.Fatalf("descriptor is not a constant global pointer: %v", descriptor.impl)
			}
			if got := descriptor.impl.Linkage(); got != llvm.LinkOnceODRLinkage {
				t.Fatalf("descriptor linkage = %v, want linkonce_odr", got)
			}
			descriptorType := prog.Elem(descriptor.Type)
			if got := prog.SizeOf(descriptorType); got != test.descriptorSize {
				t.Fatalf("descriptor size = %d, want %d", got, test.descriptorSize)
			}
			if got := prog.OffsetOf(descriptorType, 4); got != 24 {
				t.Fatalf("plainEntry offset = %d, want 24", got)
			}
			if got := prog.OffsetOf(descriptorType, 5); got != test.coroEntryOffset {
				t.Fatalf("coroEntry offset = %d, want %d", got, test.coroEntryOffset)
			}
			if got := prog.OffsetOf(descriptorType, 6); got != test.resultSizeOffset {
				t.Fatalf("resultSize offset = %d, want %d", got, test.resultSizeOffset)
			}
			if got := prog.OffsetOf(descriptorType, 7); got != test.resultAlignOffset {
				t.Fatalf("resultAlign offset = %d, want %d", got, test.resultAlignOffset)
			}
			if got, want := descriptor.impl.Alignment(), int(prog.AlignOf(descriptorType)); got != want {
				t.Fatalf("descriptor alignment = %d, want %d", got, want)
			}

			initializer := descriptor.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 8 {
				t.Fatalf("descriptor initializer is not an eight-field constant: %v", initializer)
			}
			wantFixed := []uint64{
				uint64(CoroPlainDispatchVersionV1),
				uint64(CoroPlainDispatchFlagsV1),
				0x0102030405060708,
				0x090a0b0c0d0e0f10,
			}
			for i, want := range wantFixed {
				if got := initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("descriptor field %d = %#x, want %#x", i, got, want)
				}
			}
			plain := coroPlainDispatchFunction(initializer.Operand(4))
			if plain.IsNil() || plain.Name() != fixture.thunkName {
				t.Fatalf("plainEntry = %v, want target-specific thunk %q", plain, fixture.thunkName)
			}
			if initializer.Operand(5).IsAConstantPointerNull().IsNil() {
				t.Fatalf("coroEntry is not null: %v", initializer.Operand(5))
			}
			if got, want := initializer.Operand(6).ZExtValue(), prog.SizeOf(fixture.result); got != want {
				t.Fatalf("resultSize = %d, want %d", got, want)
			}
			if got, want := initializer.Operand(7).ZExtValue(), prog.AlignOf(fixture.result); got != want {
				t.Fatalf("resultAlign = %d, want %d", got, want)
			}

			// Repeated materialization of the same target is idempotent. This is
			// needed when multiple exact SSA producers name one planned target.
			again := pkg.NewCoroPlainDispatchDescriptor(
				descriptor.Name(), fixture.descriptorOptions("plain_target", fixture.thunkName),
			)
			if again.impl.C != descriptor.impl.C {
				t.Fatal("identical descriptor materialization did not reuse the global")
			}

			// Function identity changes the symbol names, not the ABI hash.
			initializer2 := fixture.descriptor2.impl.Initializer()
			for i := 2; i <= 3; i++ {
				if got, want := initializer2.Operand(i).ZExtValue(), initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("second target hash field %d = %#x, want ABI-only hash %#x", i, got, want)
				}
			}
			plain2 := coroPlainDispatchFunction(initializer2.Operand(4))
			if plain2.IsNil() || plain2.Name() != fixture.thunkName2 || plain2.C == plain.C {
				t.Fatalf("second target did not receive a distinct thunk: %v versus %v", plain2, plain)
			}

			ir := pkg.String()
			if !strings.Contains(ir, "@plain_descriptor = linkonce_odr unnamed_addr constant") {
				t.Fatalf("descriptor is not an unnamed_addr linkonce_odr constant:\n%s", ir)
			}
			for _, thunk := range []string{fixture.thunkName, fixture.thunkName2} {
				body := coroPlainDispatchIRFunction(ir, thunk)
				if body == "" || !strings.Contains(body, "linkonce_odr") ||
					!strings.Contains(body, "(ptr ") {
					t.Fatalf("missing target-specific (ctx,args) thunk %q:\n%s", thunk, ir)
				}
				if strings.Contains(thunk, closureStub) {
					t.Fatalf("dispatch thunk reused legacy closure stub namespace: %q", thunk)
				}
			}
			if body := coroPlainDispatchIRFunction(ir, fixture.thunkName); !strings.Contains(body, "@plain_target(") {
				t.Fatalf("target-specific thunk does not directly call its one plain target:\n%s", body)
			}
			producerBody := coroPlainDispatchIRFunction(ir, "dispatch_value")
			if !strings.Contains(producerBody, "ret { ptr, ptr } { ptr @plain_descriptor, ptr null }") {
				t.Fatalf("producer did not materialize canonical {descriptor,nil} value:\n%s", producerBody)
			}

			callBody := coroPlainDispatchIRFunction(ir, "dispatch_call")
			if callBody == "" {
				t.Fatalf("missing dispatch caller:\n%s", ir)
			}
			if !regexp.MustCompile(`call void @[^\n]*AssertNilDeref[^\n]*\(i1`).MatchString(callBody) {
				t.Fatalf("nil function call does not use the recoverable Go nil-deref path:\n%s", callBody)
			}
			if got := strings.Count(callBody, "call void @llvm.trap()"); got != 2 {
				t.Fatalf("ABI guards emitted %d trap sites, want env and descriptor traps:\n%s", got, callBody)
			}
			if got := strings.Count(callBody, "unreachable"); got != 2 {
				t.Fatalf("ABI guards emitted %d unreachable terminators, want 2:\n%s", got, callBody)
			}
			for _, guard := range []string{
				"coro.dispatch.env.nonnull",
				"coro.dispatch.field.0.invalid",
				"coro.dispatch.field.1.invalid",
				"coro.dispatch.field.2.invalid",
				"coro.dispatch.field.3.invalid",
				"coro.dispatch.plain.nil",
				"coro.dispatch.coro.nonnull",
				"coro.dispatch.result.size.invalid",
				"coro.dispatch.result.align.invalid",
			} {
				if !strings.Contains(callBody, guard) {
					t.Fatalf("dispatch caller is missing guard %q:\n%s", guard, callBody)
				}
			}
			assertCall := strings.Index(callBody, "AssertNilDeref")
			envBranch := strings.Index(callBody, "br i1 %coro.dispatch.env.nonnull")
			descriptorLoad := strings.Index(callBody, "load { i32, i32, i64, i64, ptr, ptr")
			if assertCall < 0 || envBranch < 0 || descriptorLoad < 0 ||
				assertCall > envBranch || envBranch > descriptorLoad {
				t.Fatalf("descriptor is loaded before nil/env validation:\n%s", callBody)
			}
			if !regexp.MustCompile(`call i32 %[^\n]*\(ptr [^,]+, i32 `).MatchString(callBody) {
				t.Fatalf("success path has no typed (env,args)->result indirect call:\n%s", callBody)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify plain dispatch module: %v\n%s", err, ir)
			}
		})
	}
}

func TestCoroPlainDispatchV1RejectsNonExactContract(t *testing.T) {
	Initialize(InitAll)
	fixture := newCoroPlainDispatchTestFixture(t, nil)
	options := fixture.descriptorOptions("plain_target", "__llgo_stub.plain_target")
	coroPlainDispatchMustPanicContains(t, "legacy closure stub", func() {
		fixture.pkg.NewCoroPlainDispatchDescriptor("legacy_stub_descriptor", options)
	})
	options = fixture.descriptorOptions("plain_target", "unique_thunk")
	options.Flags = CoroDispatchFlagHasPlain | CoroDispatchFlagHasCoro | CoroDispatchFlagNoCapture
	coroPlainDispatchMustPanicContains(t, "exact HasPlain|NoCapture", func() {
		fixture.pkg.NewCoroPlainDispatchDescriptor("bad_flags_descriptor", options)
	})
}

func newCoroPlainDispatchTestFixture(t *testing.T, target *Target) *coroPlainDispatchTestFixture {
	t.Helper()
	prog := NewProgram(target)
	installCoroPlainDispatchTestRuntime(prog)
	pkg := prog.NewPackage("corodispatch", "coro/dispatch")
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
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	makeTarget := func(name string) Function {
		fn := pkg.NewFunc(name, signature, InGo)
		b := fn.MakeBody(1)
		b.Return(fn.Param(0))
		b.EndBuild()
		b.Dispose()
		return fn
	}
	target1 := makeTarget("plain_target")
	target2 := makeTarget("plain_target_2")
	thunkName := "__llgo_coro_func_plain_v1.target1"
	thunkName2 := "__llgo_coro_func_plain_v1.target2"
	fixture := &coroPlainDispatchTestFixture{
		prog:       prog,
		pkg:        pkg,
		signature:  signature,
		result:     result,
		hash:       hash,
		thunkName:  thunkName,
		thunkName2: thunkName2,
	}
	fixture.descriptor = pkg.NewCoroPlainDispatchDescriptor(
		"plain_descriptor", fixture.descriptorOptions(target1.Name(), thunkName),
	)
	fixture.descriptor2 = pkg.NewCoroPlainDispatchDescriptor(
		"plain_descriptor_2", fixture.descriptorOptions(target2.Name(), thunkName2),
	)

	producerSig := coroPlainDispatchTestSignature(nil, []types.Type{signature})
	producer := pkg.NewFunc("dispatch_value", producerSig, InGo)
	pb := producer.MakeBody(1)
	pb.Return(pb.MakeCoroPlainDispatchValue(signature, fixture.descriptor))
	pb.EndBuild()
	pb.Dispose()

	callerSig := coroPlainDispatchTestSignature(
		[]types.Type{signature, types.Typ[types.Uint32]},
		[]types.Type{types.Typ[types.Uint32]},
	)
	caller := pkg.NewFunc("dispatch_call", callerSig, InGo)
	cb := caller.MakeBody(1)
	ret := cb.CallCoroPlainDispatch(
		caller.Param(0), []Expr{caller.Param(1)}, CoroPlainDispatchCallOptions{
			Version: CoroPlainDispatchVersionV1,
			Flags:   CoroPlainDispatchFlagsV1,
			ABIHash: hash,
			Result:  result,
		},
	)
	cb.Return(ret)
	cb.EndBuild()
	cb.Dispose()
	return fixture
}

func (f *coroPlainDispatchTestFixture) descriptorOptions(
	targetName, thunkName string,
) CoroPlainDispatchDescriptorOptions {
	target := f.pkg.FuncOf(targetName)
	if target == nil {
		panic("missing plain dispatch test target " + targetName)
	}
	return CoroPlainDispatchDescriptorOptions{
		Version:     CoroPlainDispatchVersionV1,
		Flags:       CoroPlainDispatchFlagsV1,
		ABIHash:     f.hash,
		PlainTarget: target.Expr,
		Signature:   f.signature,
		ThunkName:   thunkName,
		Result:      f.result,
	}
}

func installCoroPlainDispatchTestRuntime(prog Program) {
	runtimePkg := types.NewPackage(PkgRuntime, "runtime")
	sig := coroPlainDispatchTestSignature([]types.Type{types.Typ[types.Bool]}, nil)
	runtimePkg.Scope().Insert(types.NewFunc(token.NoPos, runtimePkg, "AssertNilDeref", sig))
	runtimePkg.MarkComplete()
	prog.SetRuntime(runtimePkg)
}

func coroPlainDispatchIRFunction(ir, name string) string {
	marker := "@" + name + "("
	call := strings.Index(ir, marker)
	if call < 0 {
		return ""
	}
	start := strings.LastIndex(ir[:call], "define ")
	if start < 0 {
		return ""
	}
	end := strings.Index(ir[call:], "\n}\n")
	if end < 0 {
		return ""
	}
	return ir[start : call+end+3]
}

func coroPlainDispatchTestSignature(params, results []types.Type) *types.Signature {
	tuple := func(values []types.Type) *types.Tuple {
		vars := make([]*types.Var, len(values))
		for i, value := range values {
			vars[i] = types.NewVar(token.NoPos, nil, "", value)
		}
		return types.NewTuple(vars...)
	}
	return types.NewSignatureType(nil, nil, nil, tuple(params), tuple(results), false)
}

func coroPlainDispatchMustPanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatalf("operation did not panic; want substring %q", want)
		}
		if got := fmt.Sprint(value); !strings.Contains(got, want) {
			t.Fatalf("panic = %q, want substring %q", got, want)
		}
	}()
	fn()
}
