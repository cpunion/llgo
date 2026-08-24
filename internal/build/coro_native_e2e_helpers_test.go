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

package build

import (
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

// coroNativeE2ENMHasExactSymbol parses nm's whitespace-delimited symbol
// column. Darwin prefixes the physical symbol with one underscore; Linux does
// not. Exact matching is required here because PanicIndex and
// PanicWrapNilPointer must not be mistaken for the legacy Panic entry.
func coroNativeE2ENMHasExactSymbol(output, symbol string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		physical := fields[len(fields)-1]
		if physical == symbol || physical == "_"+symbol {
			return true
		}
	}
	return false
}

func TestCoroNativeE2ENMHasExactSymbol(t *testing.T) {
	const symbol = "github.com/xgo-dev/llgo/runtime/internal/runtime.Panic"
	for _, test := range []struct {
		name   string
		output string
		want   bool
	}{
		{name: "Linux", output: "0000000000401000 T " + symbol, want: true},
		{name: "Darwin", output: "0000000100001000 T _" + symbol, want: true},
		{name: "Undefined", output: "                 U " + symbol, want: true},
		{name: "PanicIndex", output: "0000000000401000 T " + symbol + "Index"},
		{name: "PanicIndexU", output: "0000000000401000 T " + symbol + "IndexU"},
		{name: "PanicWrapNilPointer", output: "0000000000401000 T " + symbol + "WrapNilPointer"},
		{name: "Empty", output: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := coroNativeE2ENMHasExactSymbol(test.output, symbol); got != test.want {
				t.Fatalf("coroNativeE2ENMHasExactSymbol() = %v, want %v\n%s", got, test.want, test.output)
			}
		})
	}
}

func coroNativeTaskContextRuntimeSources() []string {
	root := filepath.Join("..", "..", "runtime", "internal", "runtime")
	return []string{
		filepath.Join(root, "coro_task_allocation.go"),
		filepath.Join(root, "coro_task_context.go"),
		filepath.Join(root, "runtime_context.go"),
		filepath.Join(root, "runtime2.go"),
		filepath.Join(root, "proc_atomic.go"),
		filepath.Join(root, "proc_release.go"),
		filepath.Join(root, "g_pthread.go"),
		filepath.Join(root, "local_context.go"),
	}
}

// defineCoroNativeE2ENilDerefStubs keeps the deliberately closed native
// runtime islands independent of the legacy panic/printing closure. Production
// scheduler paths never pass nil here; an invalid path remains fail-stop.
func defineCoroNativeE2ENilDerefStubs(prog llssa.Program, pkg llssa.Package, abort llssa.Function) {
	pointer := types.Typ[types.UnsafePointer]
	assertNil := pkg.NewFunc(llssa.PkgRuntime+".AssertNilDeref", newSignature(
		[]types.Type{types.Typ[types.Bool]}, nil,
	), llssa.InGo)
	assertBody := assertNil.MakeBody(3)
	fail, valid := assertNil.Block(1), assertNil.Block(2)
	assertBody.If(assertNil.Param(0), fail, valid)
	assertBody.SetBlock(fail).Call(abort.Expr)
	assertBody.Return()
	assertBody.SetBlock(valid).Return()

	assertPtr := pkg.NewFunc(llssa.PkgRuntime+".AssertNilDerefPtr", newSignature(
		[]types.Type{pointer}, []types.Type{pointer},
	), llssa.InGo)
	body := assertPtr.MakeBody(1)
	body.Call(assertNil.Expr, body.BinOp(token.EQL, assertPtr.Param(0), prog.Nil(prog.VoidPtr())))
	body.Return(assertPtr.Param(0))
}

// defineCoroNativeE2EIndexPanicStubs terminates the closed scheduler fixtures
// at the Go 1.26 signed and unsigned index-panic leaves. These helpers are
// reached only after LLGo has determined that the bounds check failed.
func defineCoroNativeE2EIndexPanicStubs(pkg llssa.Package, abort llssa.Function) {
	for _, helper := range []struct {
		name      string
		indexType types.Type
	}{
		{name: "PanicIndex", indexType: types.Typ[types.Int]},
		{name: "PanicIndexU", indexType: types.Typ[types.Uint]},
	} {
		function := pkg.NewFunc(llssa.PkgRuntime+"."+helper.name, newSignature(
			[]types.Type{helper.indexType, types.Typ[types.Int]}, nil,
		), llssa.InGo)
		body := function.MakeBody(1)
		body.Call(abort.Expr)
		body.Return()
	}
}
