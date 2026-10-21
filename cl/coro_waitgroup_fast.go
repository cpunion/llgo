// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"strconv"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This versioned source proof is deliberately narrow. A changed implementation
// keeps its original managed call until its invariants are separately proved.
// The digest covers the normalized Add body, excluding comments/positions;
// canonical atomic binding, constants and physical layout are checked below.
const coroWaitGroupAddSourceDigest = "68913784afdf092fa0b5ab6552f38ec3a776657ad312f4be7aa9c277c57422ac"

type coroWaitGroupAddFastPlan struct {
	target                 *ssa.Function
	sourceDigest           string
	stateField, valueField int
}

// This proof borrows only the already frozen callable classifications and
// predicate binding. It cannot re-run frontend intrinsic classification or
// publish a competing call/effect plan.
type coroWaitGroupProofView struct {
	predicates coro.ProgramPredicateRequirements
	functions  map[*ssa.Function]coroCallableClassification
	resolve    func(*ssa.Function) (*ssa.Function, bool)
}

func (view coroWaitGroupProofView) classification(fn *ssa.Function) (coroCallableClassification, bool) {
	if view.resolve == nil {
		return coroCallableClassification{}, false
	}
	canonical, found := view.resolve(fn)
	if !found || canonical == nil {
		return coroCallableClassification{}, false
	}
	classification, frozen := view.functions[canonical]
	return classification, frozen
}

func planCoroWaitGroupAddFast(p *context, fn *ssa.Function) *coroWaitGroupAddFastPlan {
	if p == nil || p.prog == nil || p.emissionUniverse == nil || p.emissionUniverse.coroProgramIR == nil ||
		fn == nil || fn.Pkg == nil || fn.Pkg.Pkg.Path() != "sync" || fn.Name() != "Add" || hasNoInlineDirective(fn) ||
		fn.Signature.Recv() == nil || fn.Signature.Results().Len() != 0 || fn.Signature.Params().Len() != 1 ||
		!types.Identical(fn.Signature.Params().At(0).Type(), types.Typ[types.Int]) || p.prog.PointerSize() != 8 {
		return nil
	}
	binding := p.emissionUniverse.coroProgramIR.programPredicates
	if !binding.Valid() || binding.Mask&coro.ExecutionDomainProgramCapability() == 0 || binding.Values.ExecutionDomain() {
		return nil
	}
	view := coroWaitGroupProofView{
		predicates: binding,
		functions:  p.emissionUniverse.coroProgramIR.callableClassifications,
		resolve:    p.emissionUniverse.Resolve,
	}
	target := p.prog.Target()
	if target != nil && target.GOOS != "" && target.GOOS != "darwin" && target.GOOS != "linux" {
		return nil
	}
	// The normalized body pins identifier spelling, not lexical binding.
	// Its counter/waiter conversions must still name the predeclared types.
	for _, name := range []string{"int32", "uint32", "uint64"} {
		if fn.Pkg.Pkg.Scope().Lookup(name) != nil {
			return nil
		}
	}
	race := fn.Prog.ImportedPackage("internal/race")
	if race == nil {
		return nil
	}
	enabled, ok := race.Members["Enabled"].(*ssa.NamedConst)
	if !ok || enabled.Value.Value.Kind() != constant.Bool || constant.BoolVal(enabled.Value.Value) {
		return nil
	}
	flag, ok := fn.Pkg.Pkg.Scope().Lookup("waitGroupBubbleFlag").(*types.Const)
	if !ok {
		return nil
	}
	if value, exact := constant.Uint64Val(flag.Val()); !exact || value != 0x80000000 {
		return nil
	}
	recv, ok := types.Unalias(fn.Signature.Recv().Type()).(*types.Pointer)
	if !ok {
		return nil
	}
	named, ok := types.Unalias(recv.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "sync" || named.Obj().Name() != "WaitGroup" {
		return nil
	}
	wg, ok := named.Underlying().(*types.Struct)
	if !ok || wg.NumFields() != 3 || wg.Field(1).Name() != "state" || wg.Field(2).Name() != "sema" {
		return nil
	}
	at, ok := types.Unalias(wg.Field(1).Type()).(*types.Named)
	if !ok || at.Obj().Pkg() == nil || at.Obj().Pkg().Path() != "sync/atomic" || at.Obj().Name() != "Uint64" {
		return nil
	}
	physical := p.type_(named, llssa.InGo)
	atomicType := p.type_(at, llssa.InGo)
	atomicStruct, ok := at.Underlying().(*types.Struct)
	if !ok || atomicStruct.NumFields() != 3 || atomicStruct.Field(2).Name() != "v" || !types.Identical(atomicStruct.Field(2).Type(), types.Typ[types.Uint64]) || p.prog.OffsetOf(physical, 1) != 0 || p.prog.OffsetOf(atomicType, 2) != 0 || p.prog.SizeOf(atomicType) != 8 || p.prog.AlignOf(atomicType) != 8 {
		return nil
	}
	syntax, ok := fn.Syntax().(*ast.FuncDecl)
	owner := p.emissionUniverse.fnOwners[fn]
	if !ok || syntax.Body == nil || owner == nil || !coroWaitGroupAddHasExactRaceImport(owner.files, fn, syntax) {
		return nil
	}
	var body bytes.Buffer
	if format.Node(&body, token.NewFileSet(), syntax.Body) != nil {
		return nil
	}
	sum := sha256.Sum256(body.Bytes())
	if hex.EncodeToString(sum[:]) != coroWaitGroupAddSourceDigest ||
		!coroWaitGroupAddUsesCanonicalAtomic(view, fn, at) ||
		!coroWaitGroupAddBubbleQueryIsFalse(view, fn) {
		return nil
	}
	return &coroWaitGroupAddFastPlan{target: fn, sourceDigest: coroWaitGroupAddSourceDigest, stateField: 1, valueField: 2}
}

// A body digest does not bind its file-scoped import names. In particular,
// checking internal/race.Enabled says nothing about an unrelated package
// imported as race. Require the exact owning source file and import as well.
func coroWaitGroupAddHasExactRaceImport(files []*ast.File, fn *ssa.Function, syntax *ast.FuncDecl) bool {
	for _, file := range files {
		for _, declaration := range file.Decls {
			if declaration != syntax {
				continue
			}
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil || path != "internal/race" {
					continue
				}
				if spec.Name != nil && spec.Name.Name == "race" {
					return true
				}
				if spec.Name == nil {
					imported := fn.Prog.ImportedPackage(path)
					return imported != nil && imported.Pkg.Name() == "race"
				}
			}
			return false
		}
	}
	return false
}

// The source body alone cannot grant atomic authority: its Uint64.Add must
// still be the single exact receiver-field forwarder to llgo.atomicAddReturnNew.
func coroWaitGroupAddUsesCanonicalAtomic(view coroWaitGroupProofView, fn *ssa.Function, atomicType *types.Named) bool {
	var method *ssa.Function
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().IsInvoke() {
				continue
			}
			target := call.Common().StaticCallee()
			if target == nil || target.Name() != "Add" || target.Signature.Recv() == nil {
				continue
			}
			ptr, ok := types.Unalias(target.Signature.Recv().Type()).(*types.Pointer)
			if !ok || !types.Identical(ptr.Elem(), atomicType) {
				continue
			}
			if method != nil {
				return false
			}
			method = target
		}
	}
	if method != nil {
		classification, frozen := view.classification(method)
		if !frozen || !classification.classified || classification.background != llssa.InGo {
			return false
		}
	}
	if method == nil || len(method.Blocks) != 1 || len(method.Params) != 2 ||
		method.Signature.Params().Len() != 1 || method.Signature.Results().Len() != 1 ||
		!types.Identical(method.Signature.Params().At(0).Type(), types.Typ[types.Uint64]) ||
		!types.Identical(method.Signature.Results().At(0).Type(), types.Typ[types.Uint64]) {
		return false
	}
	var field *ssa.FieldAddr
	var update *ssa.Call
	var returned bool
	for _, instruction := range method.Blocks[0].Instrs {
		switch value := instruction.(type) {
		case *ssa.DebugRef:
		case *ssa.FieldAddr:
			if field != nil || value.X != method.Params[0] || value.Field != 2 {
				return false
			}
			field = value
		case *ssa.Call:
			if update != nil || field == nil || value.Common().IsInvoke() || len(value.Common().Args) != 2 || value.Common().Args[0] != field || value.Common().Args[1] != method.Params[1] {
				return false
			}
			classification, frozen := view.classification(value.Common().StaticCallee())
			if !frozen || !classification.intrinsic || classification.opcode != llgoAtomicAddReturnNew {
				return false
			}
			update = value
		case *ssa.Return:
			if update == nil || len(value.Results) != 1 || value.Results[0] != update {
				return false
			}
			returned = true
		default:
			return false
		}
	}
	return returned
}

// emitCoroWaitGroupAddFast consumes only the immutable occurrence proof.
// Failure is side-effect free; all wake, misuse, overflow, race and bubble
// cases keep the complete original coroutine call. A successful SC CAS has
// exactly the original atomic-add value and synchronization effect.
func (p *context) emitCoroWaitGroupAddFast(b llssa.Builder, args []llssa.Expr, proof *coroWaitGroupAddFastPlan) llssa.Expr {
	if proof == nil {
		return llssa.Nil
	}
	if len(args) != 2 || proof.target == nil || proof.sourceDigest != coroWaitGroupAddSourceDigest {
		panic("WaitGroup update lost frozen operands")
	}
	// Keep the nil receiver on the original structured Go panic path.
	done := b.AllocaT(p.prog.Bool())
	b.Store(done, p.prog.BoolVal(false))
	present := b.BinOp(token.NEQ, args[0], p.prog.Nil(args[0].Type))
	b.IfThen(present, func() {
		state := b.FieldAddrKnownNonNil(b.FieldAddrKnownNonNil(args[0], proof.stateField), proof.valueField)
		u64 := p.prog.Type(types.Typ[types.Uint64], llssa.InGo)
		i32 := p.prog.Type(types.Typ[types.Int32], llssa.InGo)
		z64 := p.prog.IntVal(0, u64)
		z32 := p.prog.IntVal(0, i32)
		old := b.LoadKnownNonNil(state).SetOrdering(llssa.OrderingSeqConsistent)
		delta := b.Convert(u64, args[1])
		next := b.BinOp(token.ADD, old, b.BinOp(token.SHL, delta, p.prog.IntVal(32, u64)))
		counter := b.Convert(i32, b.BinOp(token.SHR, next, p.prog.IntVal(32, u64)))
		waiters := b.BinOp(token.AND, next, p.prog.IntVal(0x7fffffff, u64))
		unbubbled := b.BinOp(token.EQL, b.BinOp(token.AND, old, p.prog.IntVal(0x80000000, u64)), z64)
		nonnegative := b.BinOp(token.GEQ, counter, z32)
		noWaiters := b.BinOp(token.EQL, waiters, z64)
		positive := b.BinOp(token.GTR, args[1], p.prog.IntVal(0, args[1].Type))
		firstAdd := b.BinOp(token.EQL, counter, b.Convert(i32, args[1]))
		misuse := b.BinOp(token.AND, b.BinOp(token.AND, b.BinOp(token.NEQ, waiters, z64), positive), firstAdd)
		noMisuse := b.BinOp(token.EQL, misuse, p.prog.BoolVal(false))
		noWake := b.BinOp(token.OR, b.BinOp(token.GTR, counter, z32), noWaiters)
		eligible := b.BinOp(token.AND, b.BinOp(token.AND, unbubbled, nonnegative), b.BinOp(token.AND, noMisuse, noWake))
		b.IfThen(eligible, func() {
			result := b.AtomicCmpXchg(state, old, next)
			b.Store(done, b.Extract(result, 1))
		})
	})
	return b.Load(done)
}

// The current source has one zero-argument bool query before its atomic add.
// Prove that the exact bound query returns false without even reading memory
// in this predicate profile; a name or a library's opaque body is insufficient.
func coroWaitGroupAddBubbleQueryIsFalse(view coroWaitGroupProofView, fn *ssa.Function) bool {
	var query *ssa.Function
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().IsInvoke() || len(call.Common().Args) != 0 {
				continue
			}
			target := call.Common().StaticCallee()
			if target == nil || target.Signature.Params().Len() != 0 || target.Signature.Results().Len() != 1 || !types.Identical(target.Signature.Results().At(0).Type(), types.Typ[types.Bool]) {
				continue
			}
			if query != nil {
				return false
			}
			query = target
		}
	}
	value, ok := coroWaitGroupPurePredicate(view, query, make(map[*ssa.Function]bool))
	return ok && value.Value != nil && value.Value.Kind() == constant.Bool && !constant.BoolVal(value.Value)
}

// Follow the existing canonical bindings through a small acyclic query chain.
// Only constants, frozen predicate intrinsics and scalar comparisons are pure.
// Loads, stores, unknown calls, cycles and an unavailable producer body reject.
func coroWaitGroupPurePredicate(view coroWaitGroupProofView, fn *ssa.Function, stack map[*ssa.Function]bool) (*ssa.Const, bool) {
	if view.resolve == nil || fn == nil || len(stack) >= 8 {
		return nil, false
	}
	canonical, found := view.resolve(fn)
	if !found || canonical == nil {
		return nil, false
	}
	fn = canonical
	if stack[fn] || fn.Signature.Params().Len() != 0 || len(fn.FreeVars) != 0 {
		return nil, false
	}
	classification, frozen := view.classification(fn)
	if !frozen {
		return nil, false
	}
	if classification.intrinsic {
		predicate := coroProgramPredicateCapability(classification.opcode)
		binding := view.predicates
		if predicate != 0 && binding.Valid() && binding.Mask&predicate != 0 {
			return ssa.NewConst(constant.MakeBool(binding.Values&predicate != 0), types.Typ[types.Bool]), true
		}
		return nil, false
	}
	if !classification.classified || classification.background != llssa.InGo {
		return nil, false
	}
	if len(fn.Blocks) == 0 {
		return nil, false
	}
	stack[fn] = true
	defer delete(stack, fn)
	values := make(map[ssa.Value]*ssa.Const)
	get := func(value ssa.Value) *ssa.Const {
		if c, ok := value.(*ssa.Const); ok {
			return c
		}
		return values[value]
	}
	block := fn.Blocks[0]
	var predecessor *ssa.BasicBlock
	visited := make(map[*ssa.BasicBlock]bool)
	for block != nil && !visited[block] {
		visited[block] = true
		var next *ssa.BasicBlock
		for _, instruction := range block.Instrs {
			switch value := instruction.(type) {
			case *ssa.DebugRef:
			case *ssa.Call:
				if value.Common().IsInvoke() || len(value.Common().Args) != 0 {
					return nil, false
				}
				result, ok := coroWaitGroupPurePredicate(view, value.Common().StaticCallee(), stack)
				if !ok {
					return nil, false
				}
				values[value] = result
			case *ssa.UnOp:
				operand := get(value.X)
				if value.Op != token.NOT || operand == nil || operand.Value == nil || operand.Value.Kind() != constant.Bool {
					return nil, false
				}
				values[value] = ssa.NewConst(constant.MakeBool(!constant.BoolVal(operand.Value)), types.Typ[types.Bool])
			case *ssa.BinOp:
				left, right := get(value.X), get(value.Y)
				if left == nil || right == nil || (value.Op != token.EQL && value.Op != token.NEQ) {
					return nil, false
				}
				equal := false
				if left.IsNil() || right.IsNil() {
					if !left.IsNil() || !right.IsNil() {
						return nil, false
					}
					equal = true
				} else {
					equal = constant.Compare(left.Value, token.EQL, right.Value)
				}
				if value.Op == token.NEQ {
					equal = !equal
				}
				values[value] = ssa.NewConst(constant.MakeBool(equal), types.Typ[types.Bool])
			case *ssa.Convert:
				operand := get(value.X)
				if operand == nil || !operand.IsNil() || !coroWaitGroupNilPointerType(value.Type()) {
					return nil, false
				}
				values[value] = ssa.NewConst(nil, value.Type())
			case *ssa.ChangeType:
				operand := get(value.X)
				if operand == nil || !operand.IsNil() || !coroWaitGroupNilPointerType(value.Type()) {
					return nil, false
				}
				values[value] = ssa.NewConst(nil, value.Type())
			case *ssa.Phi:
				var selected *ssa.Const
				for i, pred := range block.Preds {
					if pred == predecessor {
						selected = get(value.Edges[i])
					}
				}
				if selected == nil {
					return nil, false
				}
				values[value] = selected
			case *ssa.If:
				condition := get(value.Cond)
				if condition == nil || condition.Value == nil || condition.Value.Kind() != constant.Bool || len(block.Succs) != 2 {
					return nil, false
				}
				if constant.BoolVal(condition.Value) {
					next = block.Succs[0]
				} else {
					next = block.Succs[1]
				}
			case *ssa.Jump:
				if len(block.Succs) != 1 {
					return nil, false
				}
				next = block.Succs[0]
			case *ssa.Return:
				if len(value.Results) != 1 {
					return nil, false
				}
				result := get(value.Results[0])
				return result, result != nil
			default:
				return nil, false
			}
		}
		predecessor, block = block, next
	}
	return nil, false
}

func coroWaitGroupNilPointerType(typ types.Type) bool {
	switch value := typ.Underlying().(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return value.Kind() == types.UnsafePointer
	default:
		return false
	}
}
