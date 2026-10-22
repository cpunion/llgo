// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroWaitGroupWaitSourceDigest = "fe392f3d1db71f9e347c9d298b4962828ed5cb87ee180655450b147a2bf9e0b2"
const coroWaitGroupWaitLoopSourceDigest = "edc976678a45fe5e9eb86e6d9e22822f7e8ce79e77e6b9b7a1f36698906762d4"

// The direct caller may own the first scheduling opportunity without creating
// a child frame. A failed completion check calls the unchanged waiting loop,
// carrying whether that opportunity has already been consumed. In particular
// it never retries a yield or changes semaphore/race/bubble ownership.
type coroWaitGroupWaitFastPlan struct {
	target, fallback, ready *ssa.Function
	fallbackID, readyID     coro.FunctionID
	stateField, valueField  int
}

func (proof *coroWaitGroupWaitFastPlan) valid() bool {
	return proof != nil && proof.target != nil && proof.fallback != nil && proof.ready != nil &&
		proof.target != proof.fallback && proof.fallbackID != "" && proof.readyID != "" &&
		proof.stateField == 1 && proof.valueField == 2
}

// The helper identities remain tied to the same immutable logical plan.
func (proof *coroWaitGroupWaitFastPlan) validFor(whole *coro.SSAPlan) bool {
	if !proof.valid() || whole == nil {
		return false
	}
	fallback, found := whole.FunctionPlan(proof.fallback)
	ready, present := whole.FunctionPlan(proof.ready)
	return found && present && fallback.ID == proof.fallbackID && ready.ID == proof.readyID &&
		fallback.Emission == coro.EmitCoroutine && fallback.ManagedEntry == coro.ManagedEntryCoroutine && ready.HasStaticOutcome()
}

func coroWaitGroupExactBody(fn *ssa.Function, digest string) bool {
	if fn == nil || hasNoInlineDirective(fn) || len(fn.FreeVars) != 0 {
		return false
	}
	decl, ok := fn.Syntax().(*ast.FuncDecl)
	if !ok || decl.Body == nil {
		return false
	}
	var body bytes.Buffer
	if format.Node(&body, token.NewFileSet(), decl.Body) != nil {
		return false
	}
	return fmt.Sprintf("%x", sha256.Sum256(body.Bytes())) == digest
}

func planCoroWaitGroupWaitFast(p *context, whole *coro.SSAPlan, fn *ssa.Function) *coroWaitGroupWaitFastPlan {
	if p == nil || p.prog == nil || p.emissionUniverse == nil || whole == nil || fn == nil ||
		fn.Pkg == nil || fn.Pkg.Pkg.Path() != "sync" || fn.Name() != "Wait" ||
		fn.Signature.Recv() == nil || fn.Signature.Recv().Name() != "wg" ||
		fn.Signature.Params().Len() != 0 || fn.Signature.Results().Len() != 0 || len(fn.Params) != 1 ||
		!coroWaitGroupExactBody(fn, coroWaitGroupWaitSourceDigest) {
		return nil
	}
	for _, name := range []string{"true", "false", "bool"} {
		if fn.Pkg.Pkg.Scope().Lookup(name) != nil {
			return nil
		}
	}
	receiver, ok := types.Unalias(fn.Signature.Recv().Type()).(*types.Pointer)
	if !ok {
		return nil
	}
	named, ok := types.Unalias(receiver.Elem()).(*types.Named)
	if !ok {
		return nil
	}
	var add *ssa.Function
	for i := 0; i < named.NumMethods(); i++ {
		if named.Method(i).Name() == "Add" {
			add = fn.Prog.FuncValue(named.Method(i))
		}
	}
	// Reuse the complete canonical layout, Add source, race and false-bubble
	// proof. No method name alone licenses an inline atomic or skipped acquire.
	layout := planCoroWaitGroupAddFast(p, add)
	if layout == nil {
		return nil
	}
	var fallback *ssa.Function
	for _, block := range fn.Blocks {
		for _, ins := range block.Instrs {
			switch v := ins.(type) {
			case *ssa.DebugRef, *ssa.Return:
			case *ssa.Call:
				if fallback != nil || v.Common().IsInvoke() || len(v.Common().Args) != 2 || v.Common().Args[0] != fn.Params[0] {
					return nil
				}
				literal, yes := v.Common().Args[1].(*ssa.Const)
				if !yes || literal.Value == nil || literal.Value.Kind() != constant.Bool || !constant.BoolVal(literal.Value) {
					return nil
				}
				fallback = v.Common().StaticCallee()
			default:
				return nil
			}
		}
	}
	if fallback == nil || fallback.Pkg != fn.Pkg || fallback.Name() != "llgoWait" ||
		fallback.Signature.Recv() == nil || !types.Identical(fallback.Signature.Recv().Type(), receiver) ||
		fallback.Signature.Recv().Name() != "wg" || fallback.Signature.Params().Len() != 1 ||
		fallback.Signature.Params().At(0).Name() != "opportunity" ||
		!types.Identical(fallback.Signature.Params().At(0).Type(), types.Typ[types.Bool]) ||
		fallback.Signature.Results().Len() != 0 || !coroWaitGroupExactBody(fallback, coroWaitGroupWaitLoopSourceDigest) {
		return nil
	}
	fallbackPlan, ok := whole.FunctionPlan(fallback)
	if !ok || fallbackPlan.Emission != coro.EmitCoroutine || fallbackPlan.ManagedEntry != coro.ManagedEntryCoroutine {
		return nil
	}
	u := p.emissionUniverse
	view := coroWaitGroupProofView{predicates: u.coroProgramIR.programPredicates, functions: u.coroProgramIR.callableClassifications, resolve: u.Resolve}
	owner := u.fnOwners[fallback]
	decl, _ := fallback.Syntax().(*ast.FuncDecl)
	if owner == nil || !coroWaitGroupAddHasExactRaceImport(owner.files, fallback, decl) {
		return nil
	}
	atomicType := types.Unalias(named.Underlying().(*types.Struct).Field(1).Type()).(*types.Named)
	var ready *ssa.Function
	loaded := false
	for _, block := range fallback.Blocks {
		for _, ins := range block.Instrs {
			call, ok := ins.(*ssa.Call)
			if !ok || call.Common().IsInvoke() {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee == nil {
				continue
			}
			if callee.Name() == "Load" && callee.Signature.Recv() != nil {
				if !coroWaitGroupCanonicalLoad(view, callee, atomicType) {
					return nil
				}
				loaded = true
			}
			if callee.Name() != "llgoWaitGroupReadyPeer" {
				continue
			}
			if ready != nil || len(call.Common().Args) != 0 {
				return nil
			}
			site, present, err := u.CoroCallSitePlan(call)
			if err != nil || !present || site.ElidesCall() {
				return nil
			}
			ready, ok = u.Resolve(callee)
			if site.ManagedStaticTarget != nil {
				ready, ok = site.ManagedStaticTarget, true
			}
			if !ok || ready == nil {
				return nil
			}
			if ready.Pkg == nil || ready.Pkg.Pkg.Path() != "github.com/xgo-dev/llgo/runtime/internal/runtime" ||
				ready.Name() != "CoroSemaphoreReadyPeerV1" || ready.Signature.Recv() != nil || ready.Signature.Params().Len() != 0 ||
				ready.Signature.Results().Len() != 1 || !types.Identical(ready.Signature.Results().At(0).Type(), types.Typ[types.Bool]) {
				return nil
			}
		}
	}
	if ready == nil || !loaded {
		return nil
	}
	readyPlan, ok := whole.FunctionPlan(ready)
	if !ok || !readyPlan.HasStaticOutcome() {
		return nil
	}
	return &coroWaitGroupWaitFastPlan{target: fn, fallback: fallback, ready: ready, fallbackID: fallbackPlan.ID, readyID: readyPlan.ID, stateField: layout.stateField, valueField: layout.valueField}
}

func coroWaitGroupCanonicalLoad(view coroWaitGroupProofView, fn *ssa.Function, typ *types.Named) bool {
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg.Path() != "sync/atomic" || len(fn.Params) != 1 || len(fn.Blocks) != 1 ||
		fn.Signature.Recv() == nil || fn.Signature.Params().Len() != 0 || fn.Signature.Results().Len() != 1 ||
		!types.Identical(fn.Signature.Results().At(0).Type(), types.Typ[types.Uint64]) {
		return false
	}
	recv, ok := types.Unalias(fn.Signature.Recv().Type()).(*types.Pointer)
	if !ok || !types.Identical(recv.Elem(), typ) {
		return false
	}
	classification, frozen := view.classification(fn)
	if !frozen || !classification.classified || classification.background != llssa.InGo {
		return false
	}
	var field *ssa.FieldAddr
	var load *ssa.Call
	returned := false
	for _, ins := range fn.Blocks[0].Instrs {
		switch v := ins.(type) {
		case *ssa.DebugRef:
		case *ssa.FieldAddr:
			if field != nil || v.X != fn.Params[0] || v.Field != 2 {
				return false
			}
			field = v
		case *ssa.Call:
			if load != nil || field == nil || v.Common().IsInvoke() || len(v.Common().Args) != 1 || v.Common().Args[0] != field {
				return false
			}
			c, found := view.classification(v.Common().StaticCallee())
			if !found || !c.intrinsic || c.opcode != llgoAtomicLoad {
				return false
			}
			load = v
		case *ssa.Return:
			if load == nil || len(v.Results) != 1 || v.Results[0] != load {
				return false
			}
			returned = true
		default:
			return false
		}
	}
	return returned
}

func (p *context) emitCoroWaitGroupWaitFast(b llssa.Builder, args []llssa.Expr, proof *coroWaitGroupWaitFastPlan) (llssa.Expr, llssa.Expr) {
	if !proof.validFor(p.immutablePlan()) || len(args) != 1 || p.coroBody() == nil || b.Func != p.fn {
		panic("WaitGroup wait lost frozen operands or its active caller frame")
	}
	done, opportunity := b.AllocaT(p.prog.Bool()), b.AllocaT(p.prog.Bool())
	b.Store(done, p.prog.BoolVal(false))
	b.Store(opportunity, p.prog.BoolVal(true))
	b.IfThen(b.BinOp(token.NEQ, args[0], p.prog.Nil(args[0].Type)), func() {
		state := b.FieldAddrKnownNonNil(b.FieldAddrKnownNonNil(args[0], proof.stateField), proof.valueField)
		u64 := p.prog.Type(types.Typ[types.Uint64], llssa.InGo)
		i32 := p.prog.Type(types.Typ[types.Int32], llssa.InGo)
		zero := p.prog.IntVal(0, u64)
		old := b.LoadKnownNonNil(state).SetOrdering(llssa.OrderingSeqConsistent)
		b.Store(done, b.BinOp(token.EQL, old, zero))
		counter := b.Convert(i32, b.BinOp(token.SHR, old, p.prog.IntVal(32, u64)))
		eligible := b.BinOp(token.AND, b.BinOp(token.GTR, counter, p.prog.IntVal(0, i32)), b.BinOp(token.EQL, b.BinOp(token.AND, old, p.prog.IntVal(0xffffffff, u64)), zero))
		b.IfThen(eligible, func() {
			ready := p.compileCoroStaticOutcomeTargetCall(b, proof.ready, nil, true)
			b.IfThen(ready, func() {
				b.Store(opportunity, p.prog.BoolVal(false))
				p.coroBody().yieldCurrentFrame(b)
				current := b.LoadKnownNonNil(state).SetOrdering(llssa.OrderingSeqConsistent)
				b.Store(done, b.BinOp(token.EQL, current, zero))
			})
		})
	})
	return b.Load(done), b.Load(opportunity)
}
