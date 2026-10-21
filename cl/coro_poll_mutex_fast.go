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

const (
	coroPollMutexLockDigest   = "384ce72baf95c2a2e125726919e60a7c8d5d5ed492a55ef0f133c39fc32e0a0c"
	coroPollMutexUnlockDigest = "39c93a7aa7a78ec82f88b5a4516ec790e1d7b576d9e499679e0dc8752b0ab0d5"
	coroPollMutexIncrefDigest = "ae0e7fe6b225ea3b7ff1a9aaa9b1c140534ccc00a7046127fbcf751a5fc6dfce"
	coroPollMutexDecrefDigest = "a7b1d42807663ad9813f1329886fbef7c863a0edd1b8edeecc05eb131c2d819b"
)

// A successful single CAS performs precisely the original non-waiting update.
// Contention, overflow, nil receivers and wakeups retain the original call.
type coroPollMutexFastPlan struct {
	target       *ssa.Function
	sourceDigest string
	unlock       bool
	refOnly      bool
}

func (proof *coroPollMutexFastPlan) valid() bool {
	if proof == nil || proof.target == nil {
		return false
	}
	if proof.refOnly {
		if proof.unlock {
			return proof.target.Name() == "decref" && proof.sourceDigest == coroPollMutexDecrefDigest
		}
		return proof.target.Name() == "incref" && proof.sourceDigest == coroPollMutexIncrefDigest
	}
	if proof.unlock {
		return proof.target.Name() == "rwunlock" && proof.sourceDigest == coroPollMutexUnlockDigest
	}
	return proof.target.Name() == "rwlock" && proof.sourceDigest == coroPollMutexLockDigest
}

func planCoroPollMutexFast(p *context, fn *ssa.Function) *coroPollMutexFastPlan {
	if p == nil || p.prog == nil || p.emissionUniverse == nil || p.emissionUniverse.coroProgramIR == nil ||
		fn == nil || fn.Pkg == nil || fn.Pkg.Pkg.Path() != "internal/poll" ||
		(fn.Name() != "rwlock" && fn.Name() != "rwunlock" && fn.Name() != "incref" && fn.Name() != "decref") || hasNoInlineDirective(fn) || len(fn.FreeVars) != 0 ||
		fn.Signature.Recv() == nil || fn.Signature.Recv().Name() != "mu" ||
		fn.Signature.Results().Len() != 1 || !types.Identical(fn.Signature.Results().At(0).Type(), types.Typ[types.Bool]) ||
		p.prog.PointerSize() != 8 {
		return nil
	}
	unlock := fn.Name() == "rwunlock" || fn.Name() == "decref"
	refOnly := fn.Name() == "incref" || fn.Name() == "decref"
	params := 2
	if unlock {
		params = 1
	}
	if refOnly {
		params = 0
	}
	if fn.Signature.Params().Len() != params {
		return nil
	}
	for i, name := range []string{"read", "wait"}[:params] {
		parameter := fn.Signature.Params().At(i)
		if parameter.Name() != name || !types.Identical(parameter.Type(), types.Typ[types.Bool]) {
			return nil
		}
	}
	binding := p.emissionUniverse.coroProgramIR.programPredicates
	if !binding.Valid() || binding.Mask&coro.ExecutionDomainProgramCapability() == 0 || binding.Values.ExecutionDomain() {
		return nil
	}
	if target := p.prog.Target(); target != nil && target.GOOS != "" && target.GOOS != "darwin" && target.GOOS != "linux" {
		return nil
	}
	for _, name := range []string{"uint64", "uint32", "true", "false"} {
		if fn.Pkg.Pkg.Scope().Lookup(name) != nil {
			return nil
		}
	}
	for name, want := range map[string]uint64{
		"mutexClosed": 1, "mutexRLock": 2, "mutexWLock": 4, "mutexRef": 8,
		"mutexRefMask": (1<<20 - 1) << 3, "mutexRWait": 1 << 23, "mutexRMask": (1<<20 - 1) << 23,
		"mutexWWait": 1 << 43, "mutexWMask": (1<<20 - 1) << 43,
	} {
		value, ok := fn.Pkg.Pkg.Scope().Lookup(name).(*types.Const)
		if !ok {
			return nil
		}
		if got, exact := constant.Uint64Val(value.Val()); !exact || got != want {
			return nil
		}
	}
	receiver, ok := types.Unalias(fn.Signature.Recv().Type()).(*types.Pointer)
	if !ok {
		return nil
	}
	named, ok := types.Unalias(receiver.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "internal/poll" || named.Obj().Name() != "fdMutex" {
		return nil
	}
	shape, ok := named.Underlying().(*types.Struct)
	if !ok || shape.NumFields() != 3 {
		return nil
	}
	for i, name := range []string{"state", "rsema", "wsema"} {
		kind := types.Uint32
		if i == 0 {
			kind = types.Uint64
		}
		if shape.Field(i).Name() != name || !types.Identical(shape.Field(i).Type(), types.Typ[kind]) {
			return nil
		}
	}
	physical := p.type_(named, llssa.InGo)
	if p.prog.OffsetOf(physical, 0) != 0 || p.prog.SizeOf(physical) != 16 || p.prog.AlignOf(physical) != 8 {
		return nil
	}
	syntax, ok := fn.Syntax().(*ast.FuncDecl)
	owner := p.emissionUniverse.fnOwners[fn]
	if !ok || syntax.Body == nil || owner == nil || !coroPollMutexExactAtomicImport(owner.files, fn, syntax) {
		return nil
	}
	var body bytes.Buffer
	if format.Node(&body, token.NewFileSet(), syntax.Body) != nil {
		return nil
	}
	sum := sha256.Sum256(body.Bytes())
	proof := &coroPollMutexFastPlan{target: fn, sourceDigest: hex.EncodeToString(sum[:]), unlock: unlock, refOnly: refOnly}
	if !proof.valid() {
		return nil
	}
	// Consume existing frozen intrinsic authority, never classify a new call.
	view := coroWaitGroupProofView{predicates: binding, functions: p.emissionUniverse.coroProgramIR.callableClassifications, resolve: p.emissionUniverse.Resolve}
	seen := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().IsInvoke() {
				continue
			}
			target := call.Common().StaticCallee()
			if target == nil || target.Pkg == nil || target.Pkg.Pkg.Path() != "sync/atomic" {
				continue
			}
			want := llgoAtomicLoad
			if target.Name() == "CompareAndSwapUint64" {
				want = llgoAtomicCmpXchgOK
			} else if target.Name() != "LoadUint64" {
				return nil
			}
			classification, frozen := view.classification(target)
			if !frozen || !classification.intrinsic || classification.opcode != want || seen[want] {
				return nil
			}
			seen[want] = true
		}
	}
	if !seen[llgoAtomicLoad] || !seen[llgoAtomicCmpXchgOK] {
		return nil
	}
	return proof
}

func coroPollMutexExactAtomicImport(files []*ast.File, fn *ssa.Function, syntax *ast.FuncDecl) bool {
	for _, file := range files {
		for _, declaration := range file.Decls {
			if declaration != syntax {
				continue
			}
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err == nil && path == "sync/atomic" && (spec.Name == nil || spec.Name.Name == "atomic") {
					imported := fn.Prog.ImportedPackage(path)
					return spec.Name != nil || imported != nil && imported.Pkg.Name() == "atomic"
				}
			}
			return false
		}
	}
	return false
}

func (p *context) emitCoroPollMutexFast(b llssa.Builder, args []llssa.Expr, proof *coroPollMutexFastPlan, value llssa.Expr) llssa.Expr {
	return p.emitCoroPollMutexFastUpdate(b, args, proof, value, false)
}

func (p *context) emitCoroPollMutexFastUpdate(b llssa.Builder, args []llssa.Expr, proof *coroPollMutexFastPlan, value llssa.Expr, excludeDestroy bool) llssa.Expr {
	if proof == nil {
		return llssa.Nil
	}
	wantArgs := 3
	if proof.unlock {
		wantArgs = 2
	}
	if proof.refOnly {
		wantArgs = 1
	}
	if !proof.valid() || len(args) != wantArgs || value.IsNil() {
		panic("poll mutex update lost frozen operands")
	}
	done := b.AllocaT(p.prog.Bool())
	b.Store(done, p.prog.BoolVal(false))
	b.Store(value, p.prog.BoolVal(false))
	b.IfThen(b.BinOp(token.NEQ, args[0], p.prog.Nil(args[0].Type)), func() {
		state := b.FieldAddrKnownNonNil(args[0], 0)
		u64 := p.prog.Type(types.Typ[types.Uint64], llssa.InGo)
		constant := func(v uint64) llssa.Expr { return p.prog.IntVal(v, u64) }
		zero := constant(0)
		old := b.LoadKnownNonNil(state).SetOrdering(llssa.OrderingSeqConsistent)
		ref := b.BinOp(token.AND, old, constant((1<<20-1)<<3))
		var next, eligible, returned llssa.Expr
		if proof.refOnly {
			if proof.unlock {
				next = b.BinOp(token.SUB, old, constant(8))
				eligible = b.BinOp(token.NEQ, ref, zero)
				returned = b.BinOp(token.EQL, b.BinOp(token.AND, next, constant(1|((1<<20-1)<<3))), constant(1))
			} else {
				next = b.BinOp(token.ADD, old, constant(8))
				available := b.BinOp(token.EQL, b.BinOp(token.AND, old, constant(1)), zero)
				noOverflow := b.BinOp(token.NEQ, b.BinOp(token.AND, next, constant((1<<20-1)<<3)), zero)
				eligible = b.BinOp(token.AND, available, noOverflow)
				returned = p.prog.BoolVal(true)
			}
		} else if proof.unlock {
			bit := b.SelectValue(args[1], constant(2), constant(4))
			mask := b.SelectValue(args[1], constant((1<<20-1)<<23), constant((1<<20-1)<<43))
			next = b.BinOp(token.SUB, b.BinOp(token.AND_NOT, old, bit), constant(8))
			locked := b.BinOp(token.NEQ, b.BinOp(token.AND, old, bit), zero)
			referenced := b.BinOp(token.NEQ, ref, zero)
			noWaiters := b.BinOp(token.EQL, b.BinOp(token.AND, old, mask), zero)
			eligible = b.BinOp(token.AND, b.BinOp(token.AND, locked, referenced), noWaiters)
			returned = b.BinOp(token.EQL, b.BinOp(token.AND, next, constant(1|((1<<20-1)<<3))), constant(1))
		} else {
			bit := b.SelectValue(args[1], constant(2), constant(4))
			next = b.BinOp(token.ADD, b.BinOp(token.OR, old, bit), constant(8))
			available := b.BinOp(token.EQL, b.BinOp(token.AND, old, b.BinOp(token.OR, bit, constant(1))), zero)
			noOverflow := b.BinOp(token.NEQ, b.BinOp(token.AND, next, constant((1<<20-1)<<3)), zero)
			eligible = b.BinOp(token.AND, available, noOverflow)
			returned = p.prog.BoolVal(true)
		}
		if excludeDestroy && proof.unlock {
			// FD's wrapper owns destroy when the last closed reference is
			// released. Reject that update before CAS so its original slow
			// call can perform the decrement and destruction exactly once.
			eligible = b.BinOp(token.AND, eligible, b.BinOp(token.EQL, returned, p.prog.BoolVal(false)))
		}
		b.IfThen(eligible, func() {
			updated := b.AtomicCmpXchg(state, old, next)
			b.Store(done, b.Extract(updated, 1))
			b.Store(value, returned)
		})
	})
	return b.Load(done)
}
