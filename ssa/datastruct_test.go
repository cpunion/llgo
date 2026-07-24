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
	"go/importer"
	"go/types"
	"runtime"
	"strings"
	"testing"
)

func TestMapLookupTrustsRuntimeNonNilElementPointer(t *testing.T) {
	prog := NewProgram(nil)
	prog.sizes = types.SizesFor("gc", runtime.GOARCH)
	prog.SetRuntime(func() *types.Package {
		pkg, err := importer.For("source", nil).Import(PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	})
	pkg := prog.NewPackage("maplookup", "test/maplookup")
	mapType := types.NewMap(types.Typ[types.String], types.Typ[types.Int])
	params := types.NewTuple(
		types.NewVar(0, nil, "m", mapType),
		types.NewVar(0, nil, "key", types.Typ[types.String]),
	)

	oneResult := types.NewTuple(types.NewVar(0, nil, "", types.Typ[types.Int]))
	lookupOne := pkg.NewFunc("lookupOne", types.NewSignatureType(nil, nil, nil, params, oneResult, false), InGo)
	oneBuilder := lookupOne.MakeBody(1)
	oneBuilder.Return(oneBuilder.Lookup(lookupOne.Param(0), lookupOne.Param(1), false))

	twoResults := types.NewTuple(
		types.NewVar(0, nil, "", types.Typ[types.Int]),
		types.NewVar(0, nil, "", types.Typ[types.Bool]),
	)
	lookupTwo := pkg.NewFunc("lookupTwo", types.NewSignatureType(nil, nil, nil, params, twoResults, false), InGo)
	twoBuilder := lookupTwo.MakeBody(1)
	pair := twoBuilder.Lookup(lookupTwo.Param(0), lookupTwo.Param(1), true)
	twoBuilder.Return(twoBuilder.Extract(pair, 0), twoBuilder.Extract(pair, 1))

	for name, body := range map[string]string{
		"MapAccess1": lookupOne.impl.String(),
		"MapAccess2": lookupTwo.impl.String(),
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("%s lookup omitted its runtime helper:\n%s", name, body)
		}
		if strings.Contains(body, "AssertNilDeref") {
			t.Fatalf("%s lookup added a nil check after a runtime-guaranteed non-nil element pointer:\n%s", name, body)
		}
	}
}
