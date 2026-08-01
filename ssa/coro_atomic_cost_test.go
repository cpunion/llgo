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
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestVerifyCoroAtomicCostModuleRejectsUncertifiedHelper(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	functionType := llvm.FunctionType(ctx.VoidType(), nil, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	helper := llvm.AddFunction(module, "pkg.unknown", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateCall(functionType, helper, nil, "")
	builder.CreateRetVoid()
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "calls uncertified helper") {
		t.Fatalf("uncertified helper error = %v", err)
	}
}

func TestVerifyCoroAtomicCostModuleRejectsDynamicMemoryIntrinsic(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	pointerType := llvm.PointerType(ctx.Int8Type(), 0)
	functionType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{pointerType, ctx.Int64Type()}, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	memsetType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{
		pointerType, ctx.Int8Type(), ctx.Int64Type(), ctx.Int1Type(),
	}, false)
	memset := llvm.AddFunction(module, "llvm.memset.p0.i64", memsetType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateCall(memsetType, memset, []llvm.Value{
		function.Param(0), llvm.ConstInt(ctx.Int8Type(), 0, false), function.Param(1), llvm.ConstInt(ctx.Int1Type(), 0, false),
	}, "")
	builder.CreateRetVoid()
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "variable-length memory intrinsic") {
		t.Fatalf("dynamic memset error = %v", err)
	}
}

func TestVerifyCoroAtomicCostModuleRejectsCFGCycle(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	functionType := llvm.FunctionType(ctx.VoidType(), nil, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	loop := ctx.AddBasicBlock(function, "loop")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateBr(loop)
	builder.SetInsertPointAtEnd(loop)
	builder.CreateBr(loop)
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "contains a CFG cycle") {
		t.Fatalf("CFG cycle error = %v", err)
	}
}

func TestVerifyCoroAtomicCostModuleRejectsTruncatedProofMetadata(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	functionType := llvm.FunctionType(ctx.VoidType(), nil, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateRetVoid()
	module.AddNamedMetadataOperand(CoroAtomicCostMetadataName, ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), coroAtomicCostMetadataV1, false).ConstantAsMetadata(),
		ctx.MDString(function.Name()),
		llvm.ConstInt(ctx.Int64Type(), 4, false).ConstantAsMetadata(),
		llvm.ConstInt(ctx.Int32Type(), 257, false).ConstantAsMetadata(),
		ctx.MDString(strings.Repeat("a", 64)),
		llvm.ConstInt(ctx.Int32Type(), 1, false).ConstantAsMetadata(),
	}))

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "invalid atomic-cost metadata") {
		t.Fatalf("truncated proof metadata error = %v", err)
	}
}

func TestVerifyCoroAtomicCostModuleRequiresInjectedInlineAsmCapability(t *testing.T) {
	for _, test := range []struct {
		name string
		mark bool
	}{
		{name: "unmarked"},
		{name: "compiler data anchor", mark: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, module := newCoroAtomicCostTestModule(t)
			functionType := llvm.FunctionType(ctx.VoidType(), nil, false)
			function := llvm.AddFunction(module, "pkg.atomic", functionType)
			builder := ctx.NewBuilder()
			defer builder.Dispose()
			entry := ctx.AddBasicBlock(function, "entry")
			builder.SetInsertPointAtEnd(entry)
			assembly := llvm.InlineAsm(
				functionType, ".pushsection .llgo_test_anchor\n.byte 0\n.popsection", "",
				true, false, llvm.InlineAsmDialectATT, false,
			)
			call := builder.CreateCall(functionType, assembly, nil, "")
			if test.mark {
				MarkCoroAtomicBoundedCompilerCall(ctx, call, CoroAtomicCompilerDataAnchorV1)
			}
			builder.CreateRetVoid()
			addCoroAtomicCostTestMetadata(ctx, module, function.Name())

			report, err := VerifyCoroAtomicCostModule(module)
			if !test.mark {
				if err == nil || !strings.Contains(err.Error(), "indirect or inline-assembly call") {
					t.Fatalf("unmarked inline assembly error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Functions) != 1 {
				t.Fatalf("marked inline assembly report = %+v", report)
			}
		})
	}
}

func TestVerifyCoroAtomicCostModuleAcceptsConstantMemoryIntrinsic(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	pointerType := llvm.PointerType(ctx.Int8Type(), 0)
	functionType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{pointerType}, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	memsetType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{
		pointerType, ctx.Int8Type(), ctx.Int64Type(), ctx.Int1Type(),
	}, false)
	memset := llvm.AddFunction(module, "llvm.memset.p0.i64", memsetType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateCall(memsetType, memset, []llvm.Value{
		function.Param(0), llvm.ConstInt(ctx.Int8Type(), 0, false), llvm.ConstInt(ctx.Int64Type(), 32, false),
		llvm.ConstInt(ctx.Int1Type(), 0, false),
	}, "")
	builder.CreateRetVoid()
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	report, err := VerifyCoroAtomicCostModule(module)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Functions) != 1 || report.Functions[0].LLVMMaxCost < 32 {
		t.Fatalf("constant memset report = %+v", report)
	}
}

func TestVerifyCoroAtomicCostModuleAcceptsFixedIntegerMinMaxIntrinsics(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	integerType := ctx.Int64Type()
	functionType := llvm.FunctionType(integerType, []llvm.Type{integerType, integerType}, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	value := function.Param(0)
	for _, name := range []string{"llvm.umin", "llvm.umax", "llvm.smin", "llvm.smax"} {
		value = builder.CreateIntrinsic(integerType, llvm.LookupIntrinsicID(name), []llvm.Value{
			value, function.Param(1),
		}, "")
	}
	builder.CreateRet(value)
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	report, err := VerifyCoroAtomicCostModule(module)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Functions) != 1 || report.Functions[0].LLVMMaxCost < 4 {
		t.Fatalf("fixed integer min/max report = %+v", report)
	}
}

func TestVerifyCoroAtomicCostModuleRejectsCounterfeitIntegerMinMaxIntrinsic(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	integerType := ctx.Int64Type()
	functionType := llvm.FunctionType(integerType, []llvm.Type{integerType, integerType}, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	counterfeit := llvm.AddFunction(module, "llvm.umin.i64.fake", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	value := builder.CreateCall(functionType, counterfeit, []llvm.Value{function.Param(0), function.Param(1)}, "")
	builder.CreateRet(value)
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "calls uncertified helper") {
		t.Fatalf("counterfeit integer min/max error = %v", err)
	}
}

func TestVerifyCoroAtomicCostModuleRejectsMalformedIntegerMinMaxIntrinsic(t *testing.T) {
	ctx, module := newCoroAtomicCostTestModule(t)
	i64 := ctx.Int64Type()
	i32 := ctx.Int32Type()
	functionType := llvm.FunctionType(i64, []llvm.Type{i64, i32}, false)
	function := llvm.AddFunction(module, "pkg.atomic", functionType)
	malformed := llvm.AddFunction(module, "llvm.umin.i64", functionType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	entry := ctx.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	value := builder.CreateCall(functionType, malformed, []llvm.Value{function.Param(0), function.Param(1)}, "")
	builder.CreateRet(value)
	addCoroAtomicCostTestMetadata(ctx, module, function.Name())

	_, err := VerifyCoroAtomicCostModule(module)
	if err == nil || !strings.Contains(err.Error(), "calls uncertified helper") {
		t.Fatalf("malformed integer min/max error = %v", err)
	}
}

func newCoroAtomicCostTestModule(t *testing.T) (llvm.Context, llvm.Module) {
	t.Helper()
	ctx := llvm.NewContext()
	module := ctx.NewModule("atomic-cost")
	t.Cleanup(func() {
		module.Dispose()
		ctx.Dispose()
	})
	return ctx, module
}

func addCoroAtomicCostTestMetadata(ctx llvm.Context, module llvm.Module, symbol string) {
	module.AddNamedMetadataOperand(CoroAtomicCostMetadataName, ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), coroAtomicCostMetadataV1, false).ConstantAsMetadata(),
		ctx.MDString(symbol),
		llvm.ConstInt(ctx.Int64Type(), 4, false).ConstantAsMetadata(),
		llvm.ConstInt(ctx.Int32Type(), 1, false).ConstantAsMetadata(),
		ctx.MDString(strings.Repeat("a", 64)),
		llvm.ConstInt(ctx.Int32Type(), 1, false).ConstantAsMetadata(),
	}))
}
