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

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

var coroPollFDSourceDigests = map[string]string{
	"incref":      "a4d25d55a6a07a14693a5ef7b459b30ad4ca4a06e9dffaaf6f03ee8ef9c17299",
	"decref":      "df6dbbcbef3d9de8a9d6d69305c75d9ab6af070e12ed0d8f1865a5459a76c1db",
	"readLock":    "3d4b04e38435dfa38baaee2b06e422f3022663f06d4f8b6845bf750fa3d88869",
	"readUnlock":  "695130bf9f76cdc0bf51bec303c7cecb96f4d268f4261e602ba47c5a3fb91d1c",
	"writeLock":   "326b7d908a4cdbffcf12699fe66d34f2bdbeb8bedf342db88c0e5c23e1431530",
	"writeUnlock": "8d92cf7b2e534045111337739b39c98ec87af963abed42d20d3d86993db5889d",
}

// coroPollFDFastPlan lifts only the successful non-waiting mutex update out
// of an exact FD wrapper. All error, wakeup, panic and destroy paths still call
// that wrapper; no physical child frame is published by the successful CAS.
type coroPollFDFastPlan struct {
	target       *ssa.Function
	mutex        *coroPollMutexFastPlan
	sourceDigest string
	read         bool
}

func (proof *coroPollFDFastPlan) valid() bool {
	if proof == nil || proof.target == nil || proof.mutex == nil || !proof.mutex.valid() {
		return false
	}
	name := "writeLock"
	if proof.read {
		name = "readLock"
	}
	if proof.mutex.unlock {
		name = "writeUnlock"
		if proof.read {
			name = "readUnlock"
		}
	}
	if proof.mutex.refOnly {
		if proof.read {
			return false
		}
		name = "incref"
		if proof.mutex.unlock {
			name = "decref"
		}
	}
	return proof.target.Name() == name && proof.sourceDigest == coroPollFDSourceDigests[name]
}

func (proof *coroPollFDFastPlan) validCleanup(site *coroStaticCleanupSitePlan) bool {
	return proof.valid() && proof.mutex.unlock && site != nil && site.instruction != nil &&
		site.kind == coroStaticCleanupCoroutine && site.target == proof.target &&
		site.targetPlan.ID != "" && site.closure == nil && site.closureEnvironment == nil &&
		len(site.arguments) == 1
}

func planCoroPollFDFast(p *context, fn *ssa.Function) *coroPollFDFastPlan {
	if p == nil || p.prog == nil || p.emissionUniverse == nil || fn == nil ||
		fn.Pkg == nil || fn.Pkg.Pkg.Path() != "internal/poll" ||
		coroPollFDSourceDigests[fn.Name()] == "" || hasNoInlineDirective(fn) || len(fn.FreeVars) != 0 ||
		fn.Signature.Recv() == nil || fn.Signature.Recv().Name() != "fd" ||
		fn.Signature.Params().Len() != 0 || len(fn.Params) != 1 {
		return nil
	}
	unlock := fn.Name() == "readUnlock" || fn.Name() == "writeUnlock" || fn.Name() == "decref"
	refOnly := fn.Name() == "incref" || fn.Name() == "decref"
	if unlock && !refOnly {
		if fn.Signature.Results().Len() != 0 {
			return nil
		}
	} else if fn.Signature.Results().Len() != 1 ||
		!types.Identical(fn.Signature.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
		return nil
	}
	if fn.Pkg.Pkg.Scope().Lookup("nil") != nil || fn.Pkg.Pkg.Scope().Lookup("error") != nil {
		return nil
	}
	for name, want := range map[string]bool{"readlock": true, "writeLock": false, "waitLock": true} {
		value, ok := fn.Pkg.Pkg.Scope().Lookup(name).(*types.Const)
		if !ok || value.Val().Kind() != constant.Bool || constant.BoolVal(value.Val()) != want {
			return nil
		}
	}
	receiver, ok := types.Unalias(fn.Signature.Recv().Type()).(*types.Pointer)
	if !ok {
		return nil
	}
	named, ok := types.Unalias(receiver.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "internal/poll" || named.Obj().Name() != "FD" {
		return nil
	}
	shape, ok := named.Underlying().(*types.Struct)
	if !ok || shape.NumFields() == 0 || shape.Field(0).Name() != "fdmu" {
		return nil
	}
	mutexType, ok := types.Unalias(shape.Field(0).Type()).(*types.Named)
	if !ok || mutexType.Obj().Pkg() != named.Obj().Pkg() || mutexType.Obj().Name() != "fdMutex" ||
		p.prog.OffsetOf(p.type_(named, llssa.InGo), 0) != 0 {
		return nil
	}
	syntax, ok := fn.Syntax().(*ast.FuncDecl)
	if !ok || syntax.Body == nil {
		return nil
	}
	var body bytes.Buffer
	if format.Node(&body, token.NewFileSet(), syntax.Body) != nil {
		return nil
	}
	sum := sha256.Sum256(body.Bytes())
	digest := hex.EncodeToString(sum[:])
	if digest != coroPollFDSourceDigests[fn.Name()] {
		return nil
	}
	wantMethod := "rwlock"
	if unlock {
		wantMethod = "rwunlock"
	}
	if refOnly {
		wantMethod = "incref"
		if unlock {
			wantMethod = "decref"
		}
	}
	var mutex *coroPollMutexFastPlan
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().IsInvoke() {
				continue
			}
			target := call.Common().StaticCallee()
			if target == nil || target.Name() != wantMethod {
				continue
			}
			canonical, frozen := p.emissionUniverse.Resolve(target)
			if !frozen || canonical != target || mutex != nil || len(call.Common().Args) == 0 {
				return nil
			}
			address, ok := call.Common().Args[0].(*ssa.FieldAddr)
			if !ok || address.X != fn.Params[0] || address.Field != 0 {
				return nil
			}
			mutex = planCoroPollMutexFast(p, target)
			if mutex == nil || mutex.unlock != unlock || mutex.refOnly != refOnly {
				return nil
			}
		}
	}
	proof := &coroPollFDFastPlan{target: fn, mutex: mutex, sourceDigest: digest, read: fn.Name() == "readLock" || fn.Name() == "readUnlock"}
	if !proof.valid() {
		return nil
	}
	return proof
}

func (p *context) emitCoroPollFDFast(b llssa.Builder, args []llssa.Expr, proof *coroPollFDFastPlan) llssa.Expr {
	if !proof.valid() || len(args) != 1 {
		panic("FD mutex update lost frozen wrapper proof")
	}
	done := b.AllocaT(p.prog.Bool())
	b.Store(done, p.prog.BoolVal(false))
	b.IfThen(b.BinOp(token.NEQ, args[0], p.prog.Nil(args[0].Type)), func() {
		mutexArgs := []llssa.Expr{b.FieldAddrKnownNonNil(args[0], 0)}
		if !proof.mutex.refOnly {
			mutexArgs = append(mutexArgs, p.prog.BoolVal(proof.read))
		}
		if !proof.mutex.unlock && !proof.mutex.refOnly {
			mutexArgs = append(mutexArgs, p.prog.BoolVal(true))
		}
		value := b.AllocaT(p.prog.Bool())
		b.Store(done, p.emitCoroPollMutexFastUpdate(b, mutexArgs, proof.mutex, value, true))
	})
	return b.Load(done)
}
