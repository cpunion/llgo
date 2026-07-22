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

package cl

import (
	"go/ast"
	"go/types"
	"testing"

	"github.com/goplus/llgo/internal/typepatch"
	"github.com/goplus/llgo/ssa/abi"
	"github.com/goplus/llgo/ssa/ssatest"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDynamicImplementsUsesEffectivePatchedTypes(t *testing.T) {
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, "example.com/emission/p", `package p
type Type interface { Elem() Type; hidden() int }
func Invoke(value Type) Type { return value.Elem() }
`)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/p", `package p
type Type interface { Elem() Type; hidden() int }
type rtype struct{}
func (rtype) Elem() Type { return nil }
func (rtype) hidden() int { return 0 }
func Materialize() Type { return rtype{} }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, Patches{
		"example.com/emission/p": {Alt: alt.ssa, Types: typepatch.Clone(alt.types)},
	}, []EmissionPackage{{
		SSA: original.ssa, Files: []*ast.File{original.file, alt.file},
	}})
	if err != nil {
		t.Fatal(err)
	}

	invoke := original.ssa.Func("Invoke")
	var invokeCall *ssa.Call
	for _, block := range invoke.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().IsInvoke() {
				invokeCall = call
			}
		}
	}
	if invokeCall == nil {
		t.Fatal("fixture has no interface invoke")
	}
	iface, ok := invokeCall.Common().Value.Type().Underlying().(*types.Interface)
	if !ok {
		t.Fatalf("invoke receiver type = %T; want interface", invokeCall.Common().Value.Type().Underlying())
	}
	var method *ssa.Function
	for _, function := range universe.Functions() {
		if function != nil && function.Name() == "Elem" && function.Signature.Recv() != nil && function.Pkg == alt.ssa {
			method = function
			break
		}
	}
	if method == nil {
		t.Fatal("alternate rtype.Elem method is absent from frozen emission universe")
	}
	receiver := method.Signature.Recv().Type()
	if types.Implements(receiver, iface) {
		t.Fatal("fixture raw alternate receiver unexpectedly implements original invoke interface")
	}
	implements, err := universe.CoroDynamicImplements(receiver, iface)
	if err != nil {
		t.Fatal(err)
	}
	if !implements {
		t.Fatal("effective alternate receiver does not implement effective patched interface")
	}
}
