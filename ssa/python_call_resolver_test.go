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
	"strings"
	"testing"
)

func TestPythonCallResolverInterceptsOnlyPythonCAPIIdentity(t *testing.T) {
	prog := NewProgram(nil)
	t.Cleanup(prog.Dispose)
	pkg := prog.NewPackage("caller", "example.com/caller")
	python := pkg.NewFunc("Py_Helper", NoArgsNoRet, InPython)
	ordinary := pkg.NewFunc("C_Helper", NoArgsNoRet, InC)

	calls := 0
	pkg.SetResolvePythonCall(func(_ Builder, fn Expr, args []Expr) (Expr, bool) {
		calls++
		if fn.Name() != "Py_Helper" || len(args) != 0 {
			t.Fatalf("Python resolver input = %q, %d args", fn.Name(), len(args))
		}
		return Expr{}, true
	})

	caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
	b := caller.MakeBody(1)
	b.Call(ordinary.Expr)
	b.Call(python.Expr)
	b.Return()
	b.EndBuild()
	if calls != 1 {
		t.Fatalf("Python resolver calls = %d, want 1", calls)
	}
	ir := pkg.String()
	if !strings.Contains(ir, "call void @C_Helper()") {
		t.Fatalf("ordinary C call was intercepted:\n%s", ir)
	}
	if strings.Contains(ir, "call void @Py_Helper()") {
		t.Fatalf("resolved Python C-API call retained its direct call:\n%s", ir)
	}
}

func TestPythonCallResolverFallbackPreservesDirectCall(t *testing.T) {
	prog := NewProgram(nil)
	t.Cleanup(prog.Dispose)
	pkg := prog.NewPackage("caller", "example.com/caller")
	python := pkg.NewFunc("Py_Helper", NoArgsNoRet, InPython)
	calls := 0
	pkg.SetResolvePythonCall(func(_ Builder, _ Expr, _ []Expr) (Expr, bool) {
		calls++
		return Expr{}, false
	})

	caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
	b := caller.MakeBody(1)
	b.Call(python.Expr)
	b.Return()
	b.EndBuild()
	if calls != 1 {
		t.Fatalf("Python resolver calls = %d, want 1", calls)
	}
	if ir := pkg.String(); !strings.Contains(ir, "call void @Py_Helper()") {
		t.Fatalf("fallback lost direct Python C-API call:\n%s", ir)
	}
}
