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

	llssa "github.com/goplus/llgo/ssa"
)

func coroNativeTaskContextRuntimeSources() []string {
	root := filepath.Join("..", "..", "runtime", "internal", "runtime")
	return []string{
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
