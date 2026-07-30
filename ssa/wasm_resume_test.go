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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/wasmresume"
)

func TestWasmResumeABIInventoriesGoCalls(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableWasmResumeABI(true)

	pkg := prog.NewPackage("p", "example.com/p")
	goFn := pkg.NewFunc("goFn", NoArgsNoRet, InGo)
	gb := goFn.MakeBody(1)
	gb.Return()

	cFn := pkg.NewFunc("cFn", NoArgsNoRet, InC)
	caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
	b := caller.MakeBody(1)
	b.Call(goFn.Expr)
	b.Call(b.MakeClosure(goFn.Expr, nil))
	b.Call(cFn.Expr)
	b.Return()

	ir := pkg.String()
	if got := strings.Count(ir, "!"+wasmresume.CallMetadata); got != 3 {
		t.Fatalf("resumable call marker count = %d, want 3:\n%s", got, ir)
	}
	if !strings.Contains(ir, `"`+wasmresume.FunctionAttribute+`"="1"`) {
		t.Fatalf("Go functions are not marked for resumable lowering:\n%s", ir)
	}
	if !strings.Contains(ir, "@"+wasmresume.StartSymbol("__llgo_stub.goFn")) {
		t.Fatalf("closure does not reference its resumable start entry:\n%s", ir)
	}
	var foundCCall bool
	for _, line := range strings.Split(ir, "\n") {
		if strings.Contains(line, "call void @cFn") && strings.Contains(line, wasmresume.CallMetadata) {
			t.Fatalf("C call was marked resumable: %s", line)
		}
		if strings.Contains(line, "call void @cFn") {
			foundCCall = true
		}
	}
	if !foundCCall {
		t.Fatalf("C call is missing from test IR:\n%s", ir)
	}
	if got := b.directCallBackground(Builtin("len")); got != inUnknown {
		t.Fatalf("builtin call background = %d, want unknown", got)
	}
	delete(pkg.fns, cFn.Name())
	if got := b.directCallBackground(cFn.Expr); got != inUnknown {
		t.Fatalf("untracked declaration background = %d, want unknown", got)
	}
}

func TestWasmResumeABIDoesNotChangeDefaultOrNativeIR(t *testing.T) {
	tests := []struct {
		name   string
		target *Target
		enable bool
	}{
		{name: "wasm disabled", target: &Target{GOOS: "wasip1", GOARCH: "wasm"}},
		{name: "native enabled", target: &Target{GOOS: "darwin", GOARCH: "arm64"}, enable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			defer prog.Dispose()
			prog.EnableWasmResumeABI(test.enable)
			pkg := prog.NewPackage("p", "example.com/p")
			callee := pkg.NewFunc("callee", NoArgsNoRet, InGo)
			caller := pkg.NewFunc("caller", NoArgsNoRet, InGo)
			b := caller.MakeBody(1)
			b.Call(callee.Expr)
			b.Call(b.MakeClosure(callee.Expr, nil))
			b.Return()

			ir := pkg.String()
			if strings.Contains(ir, wasmresume.FunctionAttribute) ||
				strings.Contains(ir, wasmresume.CallMetadata) ||
				strings.Contains(ir, wasmresume.StartSymbol("")) {
				t.Fatalf("inactive resumable ABI changed IR:\n%s", ir)
			}
		})
	}
}

func TestWasmResumeABIClosureWithContextUsesStartEntry(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableWasmResumeABI(true)
	prog.TypeSizes(types.SizesFor("gc", "wasm"))
	prog.SetRuntime(func() *types.Package {
		pkg, err := importer.For("source", nil).Import(PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	})

	pkg := prog.NewPackage("p", "example.com/p")
	fields := []*types.Var{
		types.NewField(token.NoPos, nil, "value", types.Typ[types.Int], false),
	}
	ctxType := types.NewStruct(fields, nil)
	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.NewPointer(ctxType))
	sig := types.NewSignatureType(
		nil, nil, nil, types.NewTuple(ctx), nil, false,
	)
	inner := pkg.NewFunc("inner", sig, InGo)
	inner.MakeBody(1).Return()

	outer := pkg.NewFunc("outer", NoArgsNoRet, InGo)
	b := outer.MakeBody(1)
	b.Call(b.MakeClosure(inner.Expr, []Expr{prog.Val(42)}))
	b.Return()

	ir := pkg.String()
	if !strings.Contains(ir, "@"+wasmresume.StartSymbol("inner")) {
		t.Fatalf("capturing closure does not reference its start entry:\n%s", ir)
	}
	if strings.Contains(ir, wasmresume.StartSymbol(closureStub+"inner")) {
		t.Fatalf("capturing closure was wrapped unnecessarily:\n%s", ir)
	}
}

func TestWasmResumeABIMethodMetadataUsesStartEntries(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableWasmResumeABI(true)
	prog.TypeSizes(types.SizesFor("gc", "wasm"))
	prog.SetRuntime(func() *types.Package {
		pkg, err := importer.For("source", nil).Import(PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	})

	pkg := prog.NewPackage("p", "example.com/p")
	goPkg := types.NewPackage("example.com/p", "p")
	named := types.NewNamed(
		types.NewTypeName(token.NoPos, goPkg, "S", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	recv := types.NewVar(token.NoPos, goPkg, "", named)
	method := types.NewFunc(
		token.NoPos,
		goPkg,
		"M",
		types.NewSignatureType(recv, nil, nil, nil, nil, false),
	)
	named.AddMethod(method)

	use := pkg.NewFunc("use", NoArgsNoRet, InGo)
	b := use.MakeBody(1)
	b.abiType(named)
	b.Return()

	ir := pkg.String()
	for _, want := range []string{
		wasmresume.StartSymbol("example.com/p.(*S).M"),
		wasmresume.StartSymbol(closureStub + "example.com/p.S.M"),
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("method metadata does not reference %s:\n%s", want, ir)
		}
	}
}
