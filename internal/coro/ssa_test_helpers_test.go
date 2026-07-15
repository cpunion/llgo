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

package coro

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func buildCoroTestSSA(t *testing.T, filename, source string) (*ssa.Program, *ssa.Package) {
	return buildCoroTestSSAWithMode(t, filename, source, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
}

func buildCoroTestSSAWithMode(t *testing.T, filename, source string, mode ssa.BuilderMode) (*ssa.Program, *ssa.Package) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg := types.NewPackage("example.test/coroid", "coroid")
	ssaPkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset,
		pkg,
		[]*ast.File{file},
		mode,
	)
	if err != nil {
		t.Fatalf("BuildPackage: %v", err)
	}
	return ssaPkg.Prog, ssaPkg
}

func packageFunction(t *testing.T, pkg *ssa.Package, name string) *ssa.Function {
	t.Helper()
	member, ok := pkg.Members[name]
	if !ok {
		t.Fatalf("SSA package has no member %q", name)
	}
	fn, ok := member.(*ssa.Function)
	if !ok {
		t.Fatalf("SSA member %q has type %T, want *ssa.Function", name, member)
	}
	return fn
}

func matchingFunctions(prog *ssa.Program, match func(*ssa.Function) bool) []*ssa.Function {
	functions := make([]*ssa.Function, 0)
	for fn := range ssautil.AllFunctions(prog) {
		if fn != nil && match(fn) {
			functions = append(functions, fn)
		}
	}
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].Name() != functions[j].Name() {
			return functions[i].Name() < functions[j].Name()
		}
		return functions[i].String() < functions[j].String()
	})
	return functions
}

func functionPlanFor(t *testing.T, plan *SSAPlan, fn *ssa.Function) FunctionPlan {
	t.Helper()
	id, ok := plan.FunctionID(fn)
	if !ok {
		t.Fatalf("SSA function %q has no FunctionID", fn.Name())
	}
	got, ok := plan.BasePlan().Lookup(id)
	if !ok {
		t.Fatalf("FunctionID for %q is absent from base plan", fn.Name())
	}
	return got
}
