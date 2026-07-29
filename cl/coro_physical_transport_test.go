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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
)

func TestCoroPhysicalTransportTypeSeparatesRawCAndManagedFunctions(t *testing.T) {
	const source = `package foo

//llgo:type C
type CFunc func(int) int

type RawBox struct { Callback CFunc }

func Root(callback CFunc, box RawBox) {}
func Managed(callback func(int) int) {}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}

	root := ssaPkg.Func("Root")
	managedRoot := ssaPkg.Func("Managed")
	rawType := root.Signature.Params().At(0).Type()
	rawBoxType := root.Signature.Params().At(1).Type()
	managedType := managedRoot.Signature.Params().At(0).Type()
	pointerType := types.Typ[types.UnsafePointer]

	rawKey := coroPhysicalTransportTypeKey(universe, rawType)
	managedKey := coroPhysicalTransportTypeKey(universe, managedType)
	pointerKey := coroPhysicalTransportTypeKey(universe, pointerType)
	if rawKey == managedKey {
		t.Fatalf("raw C and managed function transports share key %q", rawKey)
	}
	if rawKey != pointerKey {
		t.Fatalf("raw C transport key = %q, opaque pointer key = %q; want the same one-word ABI", rawKey, pointerKey)
	}

	managedBoxType := types.NewStruct(
		[]*types.Var{types.NewField(token.NoPos, nil, "Callback", managedType, false)},
		[]string{""},
	)
	if rawBoxKey, managedBoxKey := coroPhysicalTransportTypeKey(universe, rawBoxType), coroPhysicalTransportTypeKey(universe, managedBoxType); rawBoxKey == managedBoxKey {
		t.Fatalf("nested raw C and managed function transports share key %q", rawBoxKey)
	}

	signature := func(params ...types.Type) *types.Signature {
		variables := make([]*types.Var, len(params))
		for index, typ := range params {
			variables[index] = types.NewParam(token.NoPos, nil, "", typ)
		}
		return types.NewSignatureType(nil, nil, nil, types.NewTuple(variables...), root.Signature.Results(), false)
	}
	plan := coro.FunctionPlan{ID: "foo.Root"}
	if err := validateCoroPhysicalSSAParameterShape(plan, root, signature(pointerType, rawBoxType), universe); err != nil {
		t.Fatalf("exact raw-C-to-pointer physical alias was rejected: %v", err)
	}
	if err := validateCoroPhysicalSSAParameterShape(plan, root, signature(managedType, rawBoxType), universe); err == nil ||
		!strings.Contains(err.Error(), "effective parameter 0") {
		t.Fatalf("raw-C-to-managed descriptor mismatch = %v, want parameter 0 rejection", err)
	}
	if err := validateCoroPhysicalSSAParameterShape(plan, root, signature(rawType, managedBoxType), universe); err == nil ||
		!strings.Contains(err.Error(), "effective parameter 1") {
		t.Fatalf("nested raw-C-to-managed descriptor mismatch = %v, want parameter 1 rejection", err)
	}
}
