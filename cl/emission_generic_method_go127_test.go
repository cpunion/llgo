//go:build go1.27 && !llgo

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
	"go/ast"
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/ssa/ssatest"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseGenericConcreteMethodABI(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/genericmethod", `package genericmethod
type Value struct{}
func (Value) Generic[T any](value T) T { return value }
func (Value) Plain() string { return "plain" }
func Box() any { return Value{} }
func Direct() int { return Value{}.Generic[int](42) }
`)
	testProg.ssa.Build()

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}

	direct := pkg.ssa.Func("Direct")
	var instance *ssa.Function
	for _, block := range direct.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil {
				continue
			}
			candidate := call.Common().StaticCallee()
			if candidate != nil && candidate.Origin() != nil && candidate.TypeArgs() != nil {
				instance = candidate
			}
		}
	}
	if instance == nil {
		t.Fatal("explicit generic method instance was not present in the SSA call graph")
	}
	if !universe.Contains(instance) {
		t.Fatalf("explicit generic method instance %v is outside the frozen emission universe", instance)
	}

	named := pkg.types.Scope().Lookup("Value").Type()
	owner := universe.packages[pkg.ssa]
	methods, err := universe.selectABITypeMethods(owner, named, pkgNormal, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) == 0 {
		t.Fatal("non-generic Plain method was omitted from the ABI method table")
	}
	for _, method := range methods {
		if method.Name() == "Generic" || method.Origin() != nil && method.Origin().Name() == "Generic" {
			t.Fatalf("generic method %v was published in the ABI method table", method)
		}
	}

	selection := testProg.ssa.MethodSets.MethodSet(named).Lookup(pkg.types, "Plain")
	if selection == nil {
		t.Fatal("Plain method selection is absent")
	}
	plain := testProg.ssa.MethodValue(selection)
	if plain == nil || !universe.Contains(plain) {
		t.Fatalf("non-generic Plain method = %v; want it in the frozen emission universe", plain)
	}
	if _, ok := named.(*types.Named); !ok {
		t.Fatalf("Value type = %T; want *types.Named", named)
	}
}
