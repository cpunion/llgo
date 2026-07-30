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

	llssa "github.com/goplus/llgo/ssa"
	llabi "github.com/goplus/llgo/ssa/abi"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/types/typeutil"
)

// walkEmissionABITypeDemand mirrors the recursive abiType references emitted
// by ssa/abitype.go. visit is called once for every distinct ABI descriptor,
// after normalize has mapped the type to the form codegen will use.
//
// A named type's extended descriptor is populated from its underlying shape;
// that shape is therefore traversed for children without becoming a separate
// descriptor. This distinction is important for local named types whose
// anonymous underlying struct can otherwise acquire unrelated promoted method
// wrappers.
func walkEmissionABITypeDemand(root types.Type, normalize func(types.Type) types.Type, visit func(types.Type) error) error {
	return walkEmissionABITypeDemandEx(root, normalize, nil, visit)
}

func walkEmissionABITypeDemandEx(root types.Type, normalize func(types.Type) types.Type, physicalMethodSignature func(types.Type) types.Type, visit func(types.Type) error) error {
	if root == nil {
		return fmt.Errorf("ABI type demand has a nil root")
	}
	var normalized typeutil.Map
	var seen typeutil.Map
	var visitABI, visitChildren, visitMethodSignature, visitPublic, visitSignatureChildren func(types.Type) error

	// Normalization can allocate an equivalent physical type (notably a local
	// named type inside a generic instance). Memoize by the pre-normalized input:
	// a recursive field that points back to that source type must reuse the same
	// physical identity instead of manufacturing an unbounded chain of fresh
	// *types.Named values.
	normalizeABI := func(typ types.Type) types.Type {
		if cached := normalized.At(typ); cached != nil {
			return cached.(types.Type)
		}
		physical := typ
		if normalize != nil {
			physical = normalize(typ)
		}
		normalized.Set(typ, physical)
		return physical
	}

	visitPublic = func(typ types.Type) error {
		return visitABI(llabi.PublicType(typ))
	}

	visitSignatureChildren = func(typ types.Type) error {
		sig, ok := types.Unalias(typ).(*types.Signature)
		if !ok {
			return fmt.Errorf("ABI method type %v is not a signature", typ)
		}
		// abiUncommonMethods and abiInterfaceImethods describe method types
		// without their receiver.
		for _, tuple := range []*types.Tuple{sig.Params(), sig.Results()} {
			for index := 0; index < tuple.Len(); index++ {
				if err := visitPublic(tuple.At(index).Type()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	visitMethodSignature = func(typ types.Type) error {
		if physicalMethodSignature != nil {
			typ = physicalMethodSignature(typ)
		}
		return visitABI(typ)
	}

	visitChildren = func(typ types.Type) error {
		switch typ := types.Unalias(typ).(type) {
		case *types.Basic:
			return nil
		case *types.Pointer:
			return visitPublic(typ.Elem())
		case *types.Chan:
			return visitPublic(typ.Elem())
		case *types.Slice:
			return visitPublic(typ.Elem())
		case *types.Array:
			elem := llabi.PublicType(typ.Elem())
			if err := visitABI(elem); err != nil {
				return err
			}
			// ArrayType contains both Elem and Slice *Type references.
			return visitABI(types.NewSlice(elem))
		case *types.Map:
			// MapType's hasher environment keeps the physical key descriptor,
			// while its Key and Elem fields expose public (non-closure) types.
			if err := visitABI(typ.Key()); err != nil {
				return err
			}
			if err := visitPublic(typ.Key()); err != nil {
				return err
			}
			if err := visitPublic(typ.Elem()); err != nil {
				return err
			}
			// The synthesized bucket descriptor has only non-embedded fields.
			// It cannot add method functions beyond the key and element demands
			// already visited here, so method materialization need not construct
			// the target-size-dependent bucket type.
			return nil
		case *types.Signature:
			return visitSignatureChildren(typ)
		case *types.Struct:
			for index := 0; index < typ.NumFields(); index++ {
				if err := visitPublic(typ.Field(index).Type()); err != nil {
					return err
				}
			}
			return nil
		case *types.Interface:
			typ.Complete()
			for index := 0; index < typ.NumMethods(); index++ {
				if err := visitMethodSignature(typ.Method(index).Type()); err != nil {
					return err
				}
			}
			return nil
		case *types.Named:
			// abiExtendedFields uses the underlying shape in-place. Do not call
			// visitABI on the underlying container itself.
			return visitChildren(typ.Underlying())
		case *types.TypeParam, *types.Union:
			return fmt.Errorf("uninstantiated type %v reached an ABI descriptor", typ)
		default:
			return fmt.Errorf("unsupported ABI descriptor type %T (%v)", typ, typ)
		}
	}

	visitABI = func(typ types.Type) error {
		typ = normalizeABI(typ)
		if typ == nil {
			return fmt.Errorf("ABI type normalization produced a nil type")
		}
		if seen.At(typ) != nil {
			return nil
		}
		seen.Set(typ, true)
		if visit != nil {
			if err := visit(typ); err != nil {
				return err
			}
		}

		unaliased := types.Unalias(typ)
		if _, pointer := unaliased.(*types.Pointer); !pointer {
			// abiCommonFields always emits PtrToThis for non-pointer types.
			if err := visitABI(types.NewPointer(typ)); err != nil {
				return err
			}
		}

		// abiUncommonMethods embeds a descriptor for every method signature.
		// Only the cases below can have an uncommon method table.
		switch underlying := unaliased.(type) {
		case *types.Named:
			if _, isInterface := underlying.Underlying().(*types.Interface); !isInterface {
				mset := types.NewMethodSet(typ)
				for index := 0; index < mset.Len(); index++ {
					if err := visitMethodSignature(mset.At(index).Type()); err != nil {
						return err
					}
				}
			}
		case *types.Struct, *types.Pointer:
			mset := types.NewMethodSet(typ)
			for index := 0; index < mset.Len(); index++ {
				if err := visitMethodSignature(mset.At(index).Type()); err != nil {
					return err
				}
			}
		}
		return visitChildren(typ)
	}

	return visitABI(root)
}

func emissionABITypeMayHaveMethods(typ types.Type) bool {
	switch typ := types.Unalias(typ).(type) {
	case *types.Named:
		_, isInterface := typ.Underlying().(*types.Interface)
		return !isInterface
	case *types.Struct, *types.Pointer:
		return true
	}
	return false
}

func (u *EmissionUniverse) functionABIContext(fn *ssa.Function, owner *preparedEmissionPackage) (*context, error) {
	if u == nil || u.goProg == nil || fn == nil || owner == nil {
		return nil, fmt.Errorf("ABI type demand requires an emission universe, function, and exact owner")
	}
	unevaluated, _ := u.frozenUnsafeLayoutUnevaluatedSSA(fn)
	callerTracking := u.callerTracking
	if callerTracking == nil {
		// Focused report tests may construct a minimal universe literal. A
		// production universe always owns one compilation-scoped cache.
		callerTracking = NewCallerTracking()
	}
	return &context{
		prog:                 u.prog,
		goFn:                 fn,
		fset:                 u.goProg.Fset,
		goProg:               u.goProg,
		goTyps:               owner.pkgTypes,
		goPkg:                owner.ssa,
		patches:              u.patches,
		loaded:               u.loadedPackages(),
		linkOnceFns:          make(map[*ssa.Function]none),
		methodNilDerefChecks: collectMethodNilDerefChecks(fn),
		unevaluatedSSA:       unevaluated,
		addrOfFieldAddrs:     owner.addrOfFieldAddrs,
		emissionUniverse:     u,
		emissionOwner:        owner,
		trackCallerFrames:    owner.sourceUsesRuntimeCaller || packageUsesRuntimeCaller(callerTracking, owner.ssa),
		runtimeCallerFuncs:   runtimeCallerFuncSet(callerTracking, owner.ssa),
		logicalCallerFuncs:   runtimeLogicalCallerFuncSet(callerTracking, owner.ssa),
	}, nil
}

func (u *EmissionUniverse) materializeABITypeDemand(fn *ssa.Function, owner *preparedEmissionPackage, root types.Type, state emissionFunctionState) error {
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return err
	}
	physicalMethodSignature := func(typ types.Type) types.Type {
		if u.prog == nil {
			return typ
		}
		// abiUncommonMethods/abiInterfaceImethods call funcType first: a Go
		// signature becomes a closure struct, whose first field is the raw
		// receiver-less declaration signature passed to abiType.
		return llabi.PublicType(u.prog.PhysicalType(typ, llssa.InGo))
	}
	return walkEmissionABITypeDemandEx(root, ctx.patchType, physicalMethodSignature, func(typ types.Type) error {
		var references []*ssa.Function
		var synchronous []*ssa.Function
		if u.prog != nil {
			for _, helper := range u.prog.ABITypeRuntimeFunctions(typ) {
				target, available, err := u.materializeRuntimeHelperReference(fn, owner, state, helper)
				if err != nil {
					return fmt.Errorf("ABI type runtime reference %q: %w", helper, err)
				}
				if available {
					references = append(references, target)
					synchronous = append(synchronous, target)
				}
			}
		}
		if emissionABITypeMayHaveMethods(typ) {
			methodState, methodFromPatch := state.state, state.fromPatch
			if exactState, exactFromPatch, known := u.typeProvenance(owner, typ); known {
				methodState, methodFromPatch = exactState, exactFromPatch
			}
			methods, err := u.selectABITypeMethods(owner, typ, methodState, methodFromPatch)
			if err != nil {
				return err
			}
			// A generated method wrapper is part of the descriptor's physical
			// definition, not merely an external symbol reference. Archives are
			// compiled independently, so the module which emits this descriptor
			// must also emit the deterministic linkonce wrapper body. Recording
			// the exact use owner here keeps ProgramIR sufficient for codegen and
			// lets the linker coalesce identical definitions across packages.
			for index, method := range methods {
				if !isEmissionGeneratedWrapper(method) {
					continue
				}
				method, err = u.addResolvedRequired(method, owner, fn, state)
				if err != nil {
					return fmt.Errorf(
						"ABI type %v generated method wrapper %q for owner %q: %w",
						typ, methods[index].Name(), owner.identity, err,
					)
				}
				methods[index] = method
			}
			references = append(references, methods...)
		}
		if err := u.recordABIMethodReferences(fn, references); err != nil {
			return err
		}
		return u.recordABISyncReferences(fn, synchronous)
	})
}

// physicalFunctionABIType mirrors context.type_: body roots first pass through
// the function-aware patcher and Go-to-raw type conversion. Recursive abiType
// references start from that raw type and only apply Program.patchType, which
// is why materializeABITypeDemand intentionally uses ctx.patchType alone.
func (u *EmissionUniverse) physicalFunctionABIType(ctx *context, typ types.Type) types.Type {
	typ = ctx.patchType(typ)
	if u.prog == nil {
		// Pure frontend tests can scan source types without an LLGo program.
		return typ
	}
	return u.prog.PhysicalType(typ, llssa.InGo)
}

// functionABITypeDemands returns exactly the root descriptors requested by the
// current SSA body's lowering. It deliberately does not scan signatures,
// values, or arbitrary operands: merely loading or selecting a field of a type
// does not emit that type's runtime ABI descriptor.
func (u *EmissionUniverse) functionABITypeDemands(fn *ssa.Function, owner *preparedEmissionPackage) ([]types.Type, error) {
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return nil, err
	}
	physical := func(typ types.Type) types.Type {
		return u.physicalFunctionABIType(ctx, typ)
	}
	var roots typeutil.Map
	var demands []types.Type
	addPhysical := func(typ types.Type) {
		if typ == nil {
			return
		}
		if roots.At(typ) == nil {
			roots.Set(typ, true)
			demands = append(demands, typ)
		}
	}
	add := func(typ types.Type) {
		if typ != nil {
			addPhysical(physical(typ))
		}
	}
	addNonEmptyPhysicalInterface := func(typ types.Type) {
		if typ == nil {
			return
		}
		iface, ok := types.Unalias(typ).Underlying().(*types.Interface)
		if !ok {
			return
		}
		iface.Complete()
		if !iface.Empty() {
			// MakeInterface and ChangeInterface pass the raw target interface,
			// rather than a possibly named interface, to unsafeInterface.
			addPhysical(iface)
		}
	}
	addNonEmptyInterface := func(typ types.Type) {
		addNonEmptyPhysicalInterface(physical(typ))
	}
	exactFunctionContext := func(target *ssa.Function) *context {
		if target == nil || target == fn || u.fnOwners == nil {
			return ctx
		}
		targetOwner := u.ownerOf(target)
		if targetOwner == nil {
			return ctx
		}
		if exact, exactErr := u.functionABIContext(target, targetOwner); exactErr == nil {
			return exact
		}
		return ctx
	}
	functionDeclType := func(target *ssa.Function, background llssa.Background) types.Type {
		if target == nil {
			return nil
		}
		targetCtx := exactFunctionContext(target)
		sig, ok := targetCtx.patchType(target.Signature).(*types.Signature)
		if !ok {
			return nil
		}
		if u.prog == nil {
			return sig
		}
		return u.prog.PhysicalFuncDecl(sig, background)
	}
	assignmentSourceType := func(value ssa.Value, background llssa.Background) types.Type {
		if function, ok := value.(*ssa.Function); ok {
			function = u.canonicalAlias(function)
			if function == nil {
				return nil
			}
			if u.isIntrinsic(function, owner) {
				if wrapper, ok := u.intrinsicWrapper(owner.ssa, function); ok {
					function = wrapper
					background = llssa.InGo
				}
			}
			return functionDeclType(function, background)
		}
		if _, constant := value.(*ssa.Const); constant && background == llssa.InC && u.prog != nil {
			return u.prog.PhysicalType(ctx.patchType(value.Type()), llssa.InC)
		}
		return physical(value.Type())
	}
	addImplicitInterfaceConversion := func(value ssa.Value, destination types.Type, background llssa.Background) {
		if value == nil || destination == nil || isUntypedNilConst(value) {
			return
		}
		source := assignmentSourceType(value, background)
		if source == nil || types.Identical(source, destination) || !types.AssignableTo(source, destination) {
			return
		}
		targetInterface, ok := types.Unalias(destination).Underlying().(*types.Interface)
		if !ok {
			return
		}
		if _, sourceIsInterface := types.Unalias(source).Underlying().(*types.Interface); !sourceIsInterface {
			// MakeInterface first converts function declarations to their public
			// closure value; physical(value.Type()) is that exact descriptor.
			add(value.Type())
		}
		addNonEmptyPhysicalInterface(targetInterface)
	}
	callCheckExprSignature := func(call *ssa.CallCommon) (*types.Signature, llssa.Background, bool) {
		if call == nil {
			return nil, llssa.InGo, false
		}
		background := llssa.InGo
		if call.IsInvoke() {
			physical, ok := u.physicalInvokeCallSignature(ctx, call)
			return physical, background, ok
		}
		switch callee := call.Value.(type) {
		case *ssa.Builtin:
			return nil, background, false
		case *ssa.Function:
			callee = u.canonicalAlias(callee)
			if callee == nil {
				return nil, background, false
			}
			_, _, ftype := ctx.funcName(callee)
			switch ftype {
			case goFunc:
				background = llssa.InGo
			case cFunc:
				background = llssa.InC
			default:
				// Python calls and compiler intrinsics do not reach
				// Builder.Call's llvmParams/checkExpr path.
				return nil, background, false
			}
			physical, ok := functionDeclType(callee, background).(*types.Signature)
			return physical, background, ok
		}
		sig, ok := ctx.patchType(call.Signature()).(*types.Signature)
		if !ok {
			return nil, background, false
		}
		if u.prog != nil {
			sig = u.prog.PhysicalFuncDecl(sig, background)
		}
		return sig, background, true
	}

	// compileFuncDecl ignores source bodies whose resolved frontend kind is
	// C, Python, or an llgo intrinsic. A declaration may still carry a body
	// (for example, an upstream Go fallback linked to llgo.skip), but none of
	// that body's ABI descriptor requests reach lowering. Keep nil-program
	// frontend tests on the ordinary Go path because they have no link table.
	if u.prog != nil {
		_, _, ftype := ctx.funcName(fn)
		if ftype != goFunc {
			return demands, nil
		}
	}

	var lowered []ssa.Instruction
	if isCgoExternSymbol(fn) {
		plan, err := u.cgoLoweringPlan(ctx, fn)
		if err != nil {
			return nil, err
		}
		hasCgoCall := false
		for _, call := range plan.calls {
			if call.compiled {
				// Only non-macro calls enter callEx. Alloc and selected _cgo_
				// pointer loads emit no runtime ABI descriptor roots.
				lowered = append(lowered, call.call)
				if instruction, ok := emissionCallIntrinsicInstruction(ctx, &call.call.Call); ok && instruction == llgoCgoCgocall {
					hasCgoCall = true
				}
			}
		}
		// cgoC2Return only constructs syscall.Errno as error after cgocall has
		// initialized p.cgoErrno. Without that call it returns the nil error
		// value directly and requests neither descriptor.
		if hasCgoCall && isCgoC2func(fn.Name()) && fn.Signature.Results().Len() == 2 {
			add(ctx.cgoErrnoType())
			addNonEmptyInterface(fn.Signature.Results().At(1).Type())
		}
	} else {
		for _, block := range fn.Blocks {
			lowered = append(lowered, block.Instrs...)
		}
	}

	for _, instruction := range lowered {
		switch instruction := instruction.(type) {
		case *ssa.MakeMap:
			add(instruction.Type())
		case *ssa.Lookup:
			add(instruction.X.Type())
		case *ssa.MapUpdate:
			add(instruction.Map.Type())
		case *ssa.Range:
			if _, ok := types.Unalias(physical(instruction.X.Type())).Underlying().(*types.Map); ok {
				add(instruction.X.Type())
			}
		case *ssa.MakeInterface:
			if !u.makeInterfaceEmitsABIType(instruction, ctx) {
				continue
			}
			add(instruction.X.Type())
			addNonEmptyInterface(instruction.Type())
		case *ssa.TypeAssert:
			add(instruction.AssertedType)
		case *ssa.ChangeInterface:
			addNonEmptyInterface(instruction.Type())
		case *ssa.Store:
			if index, ok := instruction.Addr.(*ssa.IndexAddr); ok && emissionIsVargsAlloc(ctx, index.X) {
				break
			}
			if isBlankFieldStore(instruction.Addr) {
				break
			}
			pointer, ok := types.Unalias(physical(instruction.Addr.Type())).Underlying().(*types.Pointer)
			if ok {
				addImplicitInterfaceConversion(instruction.Val, pointer.Elem(), llssa.InGo)
			}
		case *ssa.Return:
			physicalDecl, ok := functionDeclType(fn, llssa.InGo).(*types.Signature)
			if ok {
				results := physicalDecl.Results()
				for index, value := range instruction.Results {
					if index < results.Len() {
						addImplicitInterfaceConversion(value, results.At(index).Type(), llssa.InGo)
					}
				}
			}
		case *ssa.Phi:
			destination := physical(instruction.Type())
			for _, edge := range instruction.Edges {
				addImplicitInterfaceConversion(edge, destination, llssa.InGo)
			}
		case *ssa.MakeClosure:
			closure, ok := instruction.Fn.(*ssa.Function)
			if ok {
				closure = u.canonicalAlias(closure)
				if closure == nil {
					break
				}
				closureCtx := exactFunctionContext(closure)
				closureSig := closureCtx.patchType(closure.Signature).(*types.Signature)
				closureSig = llssa.FuncAddCtx(makeClosureCtx(closureCtx.goTyps, closure.FreeVars), closureSig)
				if u.prog != nil {
					closureSig = u.prog.PhysicalFuncDecl(closureSig, llssa.InGo)
				}
				contextPointer, contextOK := types.Unalias(closureSig.Params().At(0).Type()).Underlying().(*types.Pointer)
				if !contextOK {
					break
				}
				contextStruct, contextOK := types.Unalias(contextPointer.Elem()).Underlying().(*types.Struct)
				if !contextOK {
					break
				}
				for index, binding := range instruction.Bindings {
					if index < contextStruct.NumFields() {
						addImplicitInterfaceConversion(binding, contextStruct.Field(index).Type(), llssa.InGo)
					}
				}
			}
		}

		call, ok := instruction.(ssa.CallInstruction)
		if !ok {
			continue
		}
		common := call.Common()
		if builtin, ok := common.Value.(*ssa.Builtin); ok {
			if len(common.Args) == 0 {
				continue
			}
			switch builtin.Name() {
			case "delete":
				if _, ok := types.Unalias(physical(common.Args[0].Type())).Underlying().(*types.Map); ok {
					add(common.Args[0].Type())
				}
			case "clear":
				switch types.Unalias(physical(common.Args[0].Type())).Underlying().(type) {
				case *types.Map, *types.Slice:
					add(common.Args[0].Type())
				}
			}
			continue
		}
		physicalSignature, background, emitsCheckExpr := callCheckExprSignature(common)
		if !emitsCheckExpr {
			continue
		}
		params := physicalSignature.Params()
		limit := params.Len()
		if llssa.HasNameValist(physicalSignature) && limit != 0 {
			limit--
		}
		for index, argument := range common.Args {
			if index >= limit {
				break
			}
			addImplicitInterfaceConversion(argument, params.At(index).Type(), background)
		}
	}
	return demands, nil
}

// materializeABITypeDemandsOfFunction materializes only descriptors emitted by
// the function's actual lowering, replacing the former broad whole-body scan.
func (u *EmissionUniverse) materializeABITypeDemandsOfFunction(fn *ssa.Function, owner *preparedEmissionPackage, state emissionFunctionState) error {
	demands, err := u.functionABITypeDemands(fn, owner)
	if err != nil {
		return err
	}
	for _, typ := range demands {
		if err := u.materializeABITypeDemand(fn, owner, typ, state); err != nil {
			return fmt.Errorf("function %q ABI type %v: %w", fn.String(), typ, err)
		}
	}
	return nil
}

func (u *EmissionUniverse) physicalInvokeCallSignature(ctx *context, call *ssa.CallCommon) (*types.Signature, bool) {
	if u == nil || ctx == nil || call == nil || !call.IsInvoke() {
		return nil, false
	}
	sig, ok := ctx.patchType(call.Signature()).(*types.Signature)
	if !ok {
		return nil, false
	}
	if u.prog == nil {
		// cvtClosure removes the receiver before building the callable closure.
		return types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic()), true
	}
	physical := llabi.PublicType(u.prog.PhysicalType(sig, llssa.InGo))
	sig, ok = types.Unalias(physical).(*types.Signature)
	return sig, ok
}

func (u *EmissionUniverse) makeInterfaceEmitsABIType(makeInterface *ssa.MakeInterface, ctx *context) bool {
	if isUntypedNilConst(makeInterface.X) {
		return false
	}
	refs := makeInterface.Referrers()
	if refs == nil || len(*refs) != 1 {
		return true
	}
	switch ref := (*refs)[0].(type) {
	case *ssa.Store:
		index, ok := ref.Addr.(*ssa.IndexAddr)
		return !ok || !emissionIsVargsAlloc(ctx, index.X)
	case *ssa.Call:
		fn, ok := ref.Call.Value.(*ssa.Function)
		return !ok || !u.isFuncAddressIntrinsic(fn, ctx)
	}
	return true
}

func (u *EmissionUniverse) makeInterfaceConsumedByFuncAddress(makeInterface *ssa.MakeInterface, ctx *context) bool {
	if makeInterface == nil {
		return false
	}
	refs := makeInterface.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	call, ok := (*refs)[0].(*ssa.Call)
	if !ok {
		return false
	}
	function, ok := call.Call.Value.(*ssa.Function)
	return ok && u.isFuncAddressIntrinsic(function, ctx)
}

func emissionIsVargsAlloc(ctx *context, value ssa.Value) bool {
	alloc, ok := value.(*ssa.Alloc)
	if !ok || alloc.Comment != "varargs" {
		return false
	}
	pointer, ok := types.Unalias(alloc.Type()).(*types.Pointer)
	if !ok {
		return false
	}
	array, ok := types.Unalias(pointer.Elem()).(*types.Array)
	if !ok || !isAny(array.Elem()) {
		return false
	}
	refs := alloc.Referrers()
	if refs == nil || len(*refs) == 0 {
		return false
	}
	return isAllocVargs(ctx, alloc)
}

func (u *EmissionUniverse) isFuncAddressIntrinsic(fn *ssa.Function, ctx *context) bool {
	if u == nil || u.goProg == nil || ctx == nil || fn == nil {
		return false
	}
	_, name, ftype := ctx.funcName(fn)
	if ftype != llgoInstr {
		return false
	}
	instruction := llgoInstrs[name]
	return instruction == llgoFuncAddr || instruction == llgoFuncPCABI0
}
