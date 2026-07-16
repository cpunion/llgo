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
	"testing"

	"github.com/goplus/llgo/cl"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/packages"
)

func TestPrepareCoroEmissionUniverseEnablesCompleteRuntimeABI(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Pkg, files)
	pkg := &aPackage{
		Package: &packages.Package{
			ID:      llssa.PkgRuntime,
			PkgPath: llssa.PkgRuntime,
			Types:   ssaPkg.Pkg,
			Syntax:  files,
		},
		SSA: ssaPkg,
	}
	ctx := &context{
		prog:      prog,
		progSSA:   ssaPkg.Prog,
		buildConf: &Config{EnableCoroEntryResolution: true},
	}
	if err := prepareCoroEmissionUniverse(ctx, []*aPackage{pkg}); err != nil {
		t.Fatal(err)
	}
	if ctx.coroEmission == nil || !ctx.coroEmission.CompleteRuntimeABI() {
		t.Fatal("active internal/build runtime input did not enable the complete runtime ABI contract")
	}
}
