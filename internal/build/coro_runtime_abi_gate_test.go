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
	cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
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
		buildConf: &Config{},
	}
	if err := prepareCoroEmissionUniverse(ctx, []*aPackage{pkg}); err != nil {
		t.Fatal(err)
	}
	if ctx.coroEmission == nil || !ctx.coroEmission.CompleteRuntimeABI() {
		t.Fatal("active internal/build runtime input did not enable the complete runtime ABI contract")
	}
}

func TestValidatedCoroFrameRetentionABIRejectsIncompleteOrUnvalidatedRuntime(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/incomplete-runtime", `package incomplete
func Present() {}
`, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	incomplete, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: "example.com/incomplete-runtime",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.CompleteRuntimeABI() {
		t.Fatal("report universe unexpectedly claims a complete runtime ABI")
	}
	conf := &Config{
		Goos: "linux", Goarch: "amd64"}
	if !nativeCoroTimerRuntimeABI(conf) {
		t.Fatal("test configuration does not select the native timer target ABI")
	}
	if got := validatedCoroFrameRetentionABI(&context{buildConf: conf, coroEmission: incomplete}, true); got != "" {
		t.Fatalf("incomplete runtime selected frame-retention ABI %q", got)
	}

	completePkg, completeFiles := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`, nil)
	completeProg := llssa.NewProgram(nil)
	t.Cleanup(completeProg.Dispose)
	cl.ParsePkgSyntax(completeProg, completePkg.Prog.Fset, completePkg.Pkg, completeFiles)
	completeCtx := &context{
		prog: completeProg, progSSA: completePkg.Prog, buildConf: conf,
	}
	completeAPkg := &aPackage{
		Package: &packages.Package{ID: llssa.PkgRuntime, PkgPath: llssa.PkgRuntime, Types: completePkg.Pkg, Syntax: completeFiles},
		SSA:     completePkg,
	}
	if err := prepareCoroEmissionUniverse(completeCtx, []*aPackage{completeAPkg}); err != nil {
		t.Fatal(err)
	}
	if !completeCtx.coroEmission.CompleteRuntimeABI() {
		t.Fatal("active runtime universe is incomplete")
	}
	if got := validatedCoroFrameRetentionABI(completeCtx, false); got != "" {
		t.Fatalf("unvalidated runtime roots selected frame-retention ABI %q", got)
	}
	for _, target := range []Config{
		{Goos: "linux", Goarch: "arm"},
		{Goos: "wasip1", Goarch: "wasm"},
		{Goos: "linux", Goarch: "amd64", Tags: "baremetal"},
	} {
		completeCtx.buildConf = &target
		if got := validatedCoroFrameRetentionABI(completeCtx, true); got != cl.CoroFrameRetentionParkABIV2 {
			t.Fatalf("%s/%s tags %q selected frame-retention ABI %q, want %q",
				target.Goos, target.Goarch, target.Tags, got, cl.CoroFrameRetentionParkABIV2)
		}
	}
}
