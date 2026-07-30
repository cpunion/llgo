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
	if got := strings.Count(ir, "!"+wasmResumeCallMetadata); got != 2 {
		t.Fatalf("resumable call marker count = %d, want 2:\n%s", got, ir)
	}
	if !strings.Contains(ir, `"`+wasmResumeFunctionAttr+`"="1"`) {
		t.Fatalf("Go functions are not marked for resumable lowering:\n%s", ir)
	}
	var foundCCall bool
	for _, line := range strings.Split(ir, "\n") {
		if strings.Contains(line, "call void @cFn") && strings.Contains(line, wasmResumeCallMetadata) {
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
			b.Return()

			ir := pkg.String()
			if strings.Contains(ir, wasmResumeFunctionAttr) || strings.Contains(ir, wasmResumeCallMetadata) {
				t.Fatalf("inactive resumable ABI changed IR:\n%s", ir)
			}
		})
	}
}
