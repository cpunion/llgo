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

package runtime

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

const coroSysctlBridgeSource = "internal/lib/runtime/os_darwin.go"

func TestCoroSysctlBridgeSourceSelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		goos      string
		buildTags []string
		want      bool
	}{
		{name: "darwin llgo", goos: "darwin", buildTags: []string{"llgo"}, want: true},
		{name: "ordinary darwin", goos: "darwin", want: true},
		{name: "linux llgo", goos: "linux", buildTags: []string{"llgo"}},
		{name: "baremetal", goos: "darwin", buildTags: []string{"llgo", "baremetal"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "arm64"
			ctx.BuildTags = append([]string(nil), test.buildTags...)
			selected, err := ctx.MatchFile("internal/lib/runtime", "os_darwin.go")
			if err != nil {
				t.Fatal(err)
			}
			if selected != test.want {
				t.Fatalf("selected = %t, want %t", selected, test.want)
			}
		})
	}
}

func TestCoroSysctlBridgePreservesGo126PhysicalShape(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, coroSysctlBridgeSource, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		linkname string
		params   int
		results  int
		delegate string
	}{
		"internal_cpu_sysctlbynameInt32": {
			linkname: "//go:linkname internal_cpu_sysctlbynameInt32 internal/cpu.sysctlbynameInt32",
			params:   1, results: 2,
			delegate: "sysctlbynameInt32",
		},
		"internal_cpu_sysctlbynameBytes": {
			linkname: "//go:linkname internal_cpu_sysctlbynameBytes internal/cpu.sysctlbynameBytes",
			params:   2, results: 1,
			delegate: "sysctlbynameBytes",
		},
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		expect, tracked := want[function.Name.Name]
		if !tracked {
			continue
		}
		delete(want, function.Name.Name)
		if function.Body == nil || function.Type.Params.NumFields() != expect.params || function.Type.Results.NumFields() != expect.results {
			t.Errorf("%s shape params/results/body = %d/%d/%t, want %d/%d/true",
				function.Name.Name, function.Type.Params.NumFields(), function.Type.Results.NumFields(), function.Body != nil, expect.params, expect.results)
		}
		doc := ""
		if function.Doc != nil {
			doc = function.Doc.Text()
			for _, comment := range function.Doc.List {
				if strings.TrimSpace(comment.Text) == expect.linkname {
					doc = expect.linkname
				}
			}
		}
		if doc != expect.linkname {
			t.Errorf("%s lacks exact physical linkname %q", function.Name.Name, expect.linkname)
		}
		callsDelegate := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callee, ok := call.Fun.(*ast.Ident); ok && callee.Name == expect.delegate {
				callsDelegate = true
			}
			return true
		})
		if !callsDelegate {
			t.Errorf("%s does not delegate to %s", function.Name.Name, expect.delegate)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing sysctl bridge functions: %v", want)
	}
}

func TestCoroSysctlForeignCallHasExactSyncCapability(t *testing.T) {
	const path = "internal/clite/os/os.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Sysctlbyname" {
			continue
		}
		comments := make(map[string]bool)
		if function.Doc != nil {
			for _, comment := range function.Doc.List {
				comments[strings.TrimSpace(comment.Text)] = true
			}
		}
		if !comments["//llgo:coro sync"] || !comments["//go:linkname Sysctlbyname C.sysctlbyname"] || function.Body != nil {
			t.Fatalf("Sysctlbyname capability/body = sync:%t link:%t body:%t; want true/true/false",
				comments["//llgo:coro sync"], comments["//go:linkname Sysctlbyname C.sysctlbyname"], function.Body != nil)
		}
		return
	}
	t.Fatal("clite/os has no Sysctlbyname declaration")
}

func TestPatchedRuntimeSysctlUsesTypedSyncDeclaration(t *testing.T) {
	const path = "internal/lib/runtime/os_darwin.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "cos.Sysctlbyname(") {
		t.Fatal("patched runtime sysctl does not use the audited typed C declaration")
	}
	for _, forbidden := range []string{
		"llgo_rawSyscall",
		"libc_sysctlbyname_trampoline_addr",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("patched runtime sysctl retains unclassified address transport %q", forbidden)
		}
	}
}
