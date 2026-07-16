//go:build !llgo

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
	"go/token"
	"go/types"
	"testing"
)

func newRuntimeCallResolverTest(t *testing.T) (Program, Package, *types.Signature) {
	t.Helper()
	prog := NewProgram(nil)
	t.Cleanup(prog.Dispose)
	runtimePkg := types.NewPackage(PkgRuntime, PkgRuntime)
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	if alt := runtimePkg.Scope().Insert(types.NewFunc(token.NoPos, runtimePkg, "Helper", sig)); alt != nil {
		t.Fatalf("insert runtime helper returned alternate object %v", alt)
	}
	prog.SetRuntime(runtimePkg)
	return prog, prog.NewPackage("caller", "example.com/caller"), sig
}

func TestRuntimeCallResolverInterceptsOnlyRtFuncExpression(t *testing.T) {
	_, pkg, sig := newRuntimeCallResolverTest(t)
	calls := 0
	pkg.SetResolveRuntimeCall(func(b Builder, helper string, fn Expr, args []Expr) (Expr, bool) {
		calls++
		if helper != "Helper" {
			t.Fatalf("helper = %q, want Helper", helper)
		}
		// Calling the original expression from inside the resolver must bypass
		// the hook, otherwise a plain replacement would recurse forever.
		return b.Call(fn, args...), true
	})

	marked := pkg.rtFunc("Helper")
	ordinary := pkg.NewFunc(marked.Name(), sig, InGo).Expr
	if marked.Type == ordinary.Type {
		t.Fatal("rtFunc expression shares the ordinary declaration Type marker")
	}
	caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
	b := caller.MakeBody(1)
	b.Call(ordinary)
	b.Call(marked)
	b.Return()
	b.EndBuild()
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
}

func TestRuntimeCallResolverFallbackPreservesDirectCall(t *testing.T) {
	_, pkg, _ := newRuntimeCallResolverTest(t)
	calls := 0
	pkg.SetResolveRuntimeCall(func(_ Builder, helper string, _ Expr, _ []Expr) (Expr, bool) {
		calls++
		if helper != "Helper" {
			t.Fatalf("helper = %q, want Helper", helper)
		}
		return Nil, false
	})

	caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
	b := caller.MakeBody(1)
	b.Call(pkg.rtFunc("Helper"))
	b.Return()
	b.EndBuild()
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
}
