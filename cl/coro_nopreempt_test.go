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
	"strings"
	"testing"
)

func TestEmissionUniverseFreezesExactNoPreemptDirective(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nopreempt", `package nopreempt
//llgo:nopreempt
func Loop() { for {} }
func Plain() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	certificate, certified, err := universe.CoroNoPreemptCertificate(pkg.ssa.Func("Loop"))
	if err != nil || !certified || len(certificate) != 64 {
		t.Fatalf("Loop no-preempt certificate = %q, %t, %v", certificate, certified, err)
	}
	if certificate, certified, err := universe.CoroNoPreemptCertificate(pkg.ssa.Func("Plain")); err != nil || certified || certificate != "" {
		t.Fatalf("Plain no-preempt certificate = %q, %t, %v; want absent", certificate, certified, err)
	}
}

func TestEmissionUniverseFreezesExactNoUnwindDirective(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nounwind", `package nounwind
//llgo:nounwind
func Load(value *int) int { return *value }
func Plain() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	certificate, certified, err := universe.CoroNoUnwindCertificate(pkg.ssa.Func("Load"))
	if err != nil || !certified || len(certificate) != 64 {
		t.Fatalf("Load no-unwind certificate = %q, %t, %v", certificate, certified, err)
	}
	if certificate, certified, err := universe.CoroNoUnwindCertificate(pkg.ssa.Func("Plain")); err != nil || certified || certificate != "" {
		t.Fatalf("Plain no-unwind certificate = %q, %t, %v; want absent", certificate, certified, err)
	}
}

func TestEmissionUniverseRejectsMalformedNoPreemptDirective(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "arguments",
			source: `package nopreemptbad
//llgo:nopreempt unsafe
func Bad() {}
`,
			want: "accepts no arguments",
		},
		{
			name: "bodyless",
			source: `package nopreemptbad
//llgo:nopreempt
func Bad()
`,
			want: "bodyful Go function",
		},
		{
			name: "nounwind arguments",
			source: `package nopreemptbad
//llgo:nounwind unsafe
func Bad() {}
`,
			want: "accepts no arguments",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/nopreemptbad"+test.name, test.source)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.want)
			}
		})
	}
}
