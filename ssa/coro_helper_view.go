/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package ssa

import (
	"crypto/sha256"
	"fmt"
	"go/types"
	"strings"

	"github.com/xgo-dev/llvm"
)

// CoroHelperViewArgument is one already typed, compiler-owned ABI projection.
// Exactly one of Parameter (nonnegative) and Constant is present. Fields
// projects struct values unless the exact borrowed Environment is supplied;
// it never follows pointers stored in that context or mutable capture cells.
// The final conversion may retag an identical LLVM representation, not perform
// a numeric conversion, allocation, source call, or runtime type lookup.
type CoroHelperViewArgument struct {
	Parameter int
	Constant  Expr
	Fields    []int
	// Environment is the exact compiler-bound context pointer type (a closure
	// environment or boxed value receiver). Only the existing explicit env
	// word may be read, along the checked value-field path. The frontend must
	// prove the context immutable and borrowed through helper completion.
	Environment Type
	// ResultState is the exact immutable invocation record retained by the
	// owned result guard. Its consumer has already checked RequiresState and
	// a non-nil binding before invoking this view.
	ResultState Type
}

const coroHelperViewAttribute = "llgo.coro.helper.view.v1"

// NewCoroHelperView creates a pure physical ABI view of an existing helper.
// Prefix is 0 for plain, 2 for (g,out)->handle, or 3 for the outcome prefix.
// Those words are forwarded unchanged. The remaining target arguments are
// selected from the view's source parameters by the supplied typed mapping.
//
// This operation does not grant a resource capability or infer an effect. Its
// frontend must bind the real helper's frozen plan and lifetime proof. A view
// owns no coroutine frame, await, result storage, cancellation state or hidden
// closure environment; the real helper remains the sole logical activation.
func (p Package) NewCoroHelperView(name string, signature *types.Signature, target Expr, prefix int, arguments []CoroHelperViewArgument) Function {
	fail := func(reason string) { panic("ssa: coroutine helper view " + reason) }
	if name == "" || signature == nil || target.IsNil() || name == target.Name() ||
		validateCoroDispatchSignature(signature) != nil || signature.Variadic() {
		fail("requires an exact name, signature and target")
	}
	callee := p.FuncOf(target.Name())
	if target.kind != vkFuncDecl || callee == nil || callee.impl.C != target.impl.C ||
		callee.NeedsEnv() || target.impl.GlobalParent().C != p.mod.C {
		fail("requires a context-free target declaration in the same module")
	}
	signature = p.Prog.PhysicalFuncDecl(signature, InGo)
	targetSignature, ok := target.RawType().(*types.Signature)
	if !ok || validateCoroDispatchSignature(targetSignature) != nil || targetSignature.Variadic() || signature.Params().Len() <= prefix ||
		targetSignature.Params().Len() != prefix+len(arguments) ||
		!types.Identical(signature.Results(), targetSignature.Results()) {
		fail("has incompatible physical signatures")
	}
	switch prefix {
	case 0, 3:
		if signature.Results().Len() != 0 {
			fail("plain and outcome views must return void")
		}
	case 2:
		if signature.Results().Len() != 1 ||
			!types.Identical(signature.Results().At(0).Type(), types.Typ[types.UnsafePointer]) {
			fail("coroutine view must return the target handle")
		}
	default:
		fail("has an unsupported managed prefix")
	}
	for index := 0; index < prefix; index++ {
		if !types.Identical(signature.Params().At(index).Type(), types.Typ[types.UnsafePointer]) ||
			!types.Identical(targetSignature.Params().At(index).Type(), types.Typ[types.UnsafePointer]) {
			fail("must preserve exact task, output and completion prefix words")
		}
	}
	if len(arguments) == 0 || arguments[0].Parameter != 0 || !arguments[0].Constant.IsNil() || len(arguments[0].Fields) != 0 || arguments[0].Environment != nil || arguments[0].ResultState != nil ||
		!types.Identical(signature.Params().At(prefix).Type(), targetSignature.Params().At(prefix).Type()) {
		fail("must pass the same exact typed resource output cell")
	}
	if _, pointer := signature.Params().At(prefix).Type().(*types.Pointer); !pointer {
		fail("requires a typed resource output pointer")
	}
	var identity strings.Builder
	var environment Type
	fmt.Fprintf(&identity, "%s\x00%d", target.Name(), prefix)
	for index, argument := range arguments {
		parameter, literal := argument.Parameter >= 0, !argument.Constant.IsNil()
		if parameter == literal || literal && (len(argument.Fields) != 0 || argument.Environment != nil || argument.ResultState != nil) {
			fail("argument must select exactly one parameter or immutable literal")
		}
		var typ Type
		if parameter {
			if argument.Parameter >= signature.Params().Len()-prefix {
				fail("parameter projection is outside the source signature")
			}
			typ = p.Prog.Type(signature.Params().At(prefix+argument.Parameter).Type(), InGo)
		} else {
			if argument.Constant.Type == nil || !coroHelperViewConstantOwned(p, argument.Constant.impl) {
				fail("literal projection must not capture an instruction")
			}
			typ = argument.Constant.Type
		}
		if argument.ResultState != nil {
			pointer, ok := types.Unalias(argument.ResultState.RawType()).(*types.Pointer)
			output, outputOK := types.Unalias(typ.RawType()).(*types.Pointer)
			if !ok || !outputOK || argument.Parameter != 0 || argument.Environment != nil ||
				argument.ResultState.ll.Context().C != p.Prog.ctx.C {
				fail("result state requires its exact owned output pointer and frozen record")
			}
			if _, tuple := types.Unalias(output.Elem()).Underlying().(*types.Struct); !tuple {
				fail("result state requires an exact public result tuple")
			}
			if _, record := types.Unalias(pointer.Elem()).Underlying().(*types.Struct); !record {
				fail("result state requires an immutable typed record")
			}
			typ = p.Prog.Type(pointer.Elem(), InGo)
			fmt.Fprintf(&identity, "\x00state:%s", typ.ll.String())
		}
		if argument.Environment != nil {
			pointer, ok := types.Unalias(argument.Environment.RawType()).(*types.Pointer)
			if !ok || argument.Environment.ll.Context().C != p.Prog.ctx.C || argument.Parameter != 1 ||
				!types.Identical(typ.RawType(), types.Typ[types.UnsafePointer]) {
				fail("environment projection requires its exact typed explicit env word")
			}
			if environment != nil && !types.Identical(environment.RawType(), argument.Environment.RawType()) {
				fail("environment projections require one frozen layout")
			}
			environment = argument.Environment
			typ = p.Prog.Type(pointer.Elem(), InGo)
			fmt.Fprintf(&identity, "\x00env:%s", typ.ll.String())
		}
		for _, field := range argument.Fields {
			structure, ok := types.Unalias(typ.RawType()).Underlying().(*types.Struct)
			if !ok || field < 0 || field >= structure.NumFields() {
				fail("only supports in-range struct-value field projections")
			}
			typ = p.Prog.Type(structure.Field(field).Type(), InGo)
		}
		want := p.Prog.Type(targetSignature.Params().At(prefix+index).Type(), InGo)
		if !coroHelperViewRetagCompatible(typ.ll, want.ll) {
			fail("argument conversion changes its physical representation")
		}
		fmt.Fprintf(&identity, "\x00%d:%v", argument.Parameter, argument.Fields)
		if literal {
			fmt.Fprintf(&identity, ":%s", argument.Constant.impl.String())
		}
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(identity.String())))
	view := p.NewFuncEx(name, signature, InGo, false, true)
	if !types.Identical(view.RawType(), signature) || view.NeedsEnv() {
		fail("symbol conflicts with another physical signature")
	}
	if view.HasBody() {
		attribute := view.impl.GetStringAttributeAtIndex(-1, coroHelperViewAttribute)
		if attribute.IsNil() || attribute.GetStringValue() != fingerprint {
			fail("symbol conflicts with another forwarding recipe")
		}
		return view
	}
	view.impl.AddFunctionAttr(p.mod.Context().CreateStringAttribute(coroHelperViewAttribute, fingerprint))
	view.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	view.impl.SetUnnamedAddr(true)
	view.Inline(AlwaysInline)
	// An independently compiled consumer may discover this entry only by a
	// symbol in the producer's archive metadata, not a local LLVM use. Keep
	// its definition through object emission, but let final-link dead stripping
	// discard unused views: this is not an llvm.used runtime registration root.
	p.markLLVMUsed(view.impl)
	b := view.MakeBody(1)
	args := make([]Expr, 0, prefix+len(arguments))
	for index := 0; index < prefix; index++ {
		args = append(args, view.PhysicalParam(index))
	}
	for index, argument := range arguments {
		value := argument.Constant
		if argument.Parameter >= 0 {
			value = view.PhysicalParam(prefix + argument.Parameter)
		}
		if argument.ResultState != nil {
			layout := p.Prog.NewCoroDeliveryStorage(p.Prog.Elem(value.Type))
			value = b.Convert(argument.ResultState, b.LoadKnownNonNil(layout.StateAddress(b, value)))
			for _, field := range argument.Fields {
				value = b.FieldAddrKnownNonNil(value, field)
			}
			value = b.LoadKnownNonNil(value)
		} else if argument.Environment != nil {
			value = b.Convert(argument.Environment, value)
			for _, field := range argument.Fields {
				value = b.FieldAddrKnownNonNil(value, field)
			}
			value = b.LoadKnownNonNil(value)
		} else {
			for _, field := range argument.Fields {
				value = b.Field(value, field)
			}
		}
		want := p.Prog.Type(targetSignature.Params().At(prefix+index).Type(), InGo)
		if !types.Identical(value.RawType(), want.RawType()) {
			value = b.ChangeType(want, value)
		}
		args = append(args, value)
	}
	result := b.Call(target, args...)
	// Do not mark this call coro-elide-safe: the target child outlives this
	// ordinary native activation. Its existing managed caller owns that proof.
	coroPlainDispatchReturn(b, signature, result)
	b.EndBuild()
	b.Dispose()
	return view
}

func coroHelperViewRetagCompatible(source, target llvm.Type) bool {
	if source.C == target.C {
		return true
	}
	if source.TypeKind() != llvm.StructTypeKind || target.TypeKind() != llvm.StructTypeKind ||
		source.IsStructPacked() != target.IsStructPacked() {
		return false
	}
	left, right := source.StructElementTypes(), target.StructElementTypes()
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].C != right[index].C {
			return false
		}
	}
	return true
}

func coroHelperViewConstantOwned(p Package, value llvm.Value) bool {
	if !value.IsConstant() {
		return false
	}
	if !value.IsAGlobalValue().IsNil() {
		return value.GlobalParent().C == p.mod.C
	}
	for index := 0; index < value.OperandsCount(); index++ {
		if !coroHelperViewConstantOwned(p, value.Operand(index)) {
			return false
		}
	}
	return true
}
