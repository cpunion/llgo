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

func TestABIMethodFuncUsesSignatureAwareLinkResolver(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()

	typesPkg := types.NewPackage("example.com/methodlink", "methodlink")
	receiverType := types.NewNamed(types.NewTypeName(token.NoPos, typesPkg, "Receiver", nil), types.NewStruct(nil, nil), nil)
	receiver := types.NewVar(token.NoPos, typesPkg, "", receiverType)
	sig := types.NewSignature(receiver, nil, nil, false)
	methodObject := types.NewFunc(token.NoPos, typesPkg, "M", sig)
	pkg := prog.NewPackage("methodlink", typesPkg.Path())

	legacyCalls := 0
	pkg.SetResolveLinkname(func(name string) string {
		legacyCalls++
		return name + "$legacy"
	})
	methodCalls := 0
	pkg.SetResolveMethodLinkname(func(name string, method *types.Func, got *types.Signature) string {
		methodCalls++
		if method != methodObject {
			t.Fatalf("method resolver object = %p, want exact %p", method, methodObject)
		}
		if got != sig {
			t.Fatalf("method resolver signature = %p, want exact %p", got, sig)
		}
		return name + "$method"
	})

	b := &aBuilder{Pkg: pkg, Prog: prog}
	method := b.abiMethodFunc(false, typesPkg, methodObject, sig)
	if methodCalls != 1 || legacyCalls != 0 {
		t.Fatalf("method/legacy resolver calls = %d/%d, want 1/0", methodCalls, legacyCalls)
	}
	want := FuncName(typesPkg, "M", sig.Recv(), false) + "$method"
	if got := method.Name(); got != want {
		t.Fatalf("ABI method symbol = %q, want %q", got, want)
	}
}
