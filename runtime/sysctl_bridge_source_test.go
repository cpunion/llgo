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

const coroSysctlBridgeSource = "internal/runtime/cpu_sysctl_darwin_llgo.go"

func TestCoroSysctlBridgeSourceSelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		goos      string
		buildTags []string
		want      bool
	}{
		{name: "darwin llgo", goos: "darwin", buildTags: []string{"llgo"}, want: true},
		{name: "ordinary darwin", goos: "darwin"},
		{name: "linux llgo", goos: "linux", buildTags: []string{"llgo"}},
		{name: "baremetal", goos: "darwin", buildTags: []string{"llgo", "baremetal"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "arm64"
			ctx.BuildTags = append([]string(nil), test.buildTags...)
			selected, err := ctx.MatchFile("internal/runtime", "cpu_sysctl_darwin_llgo.go")
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
		indices  map[string]bool
	}{
		"internalCPUSysctlbynameInt32": {
			linkname: "//go:linkname internalCPUSysctlbynameInt32 internal/cpu.sysctlbynameInt32",
			params:   1, results: 2, indices: map[string]bool{"name": true},
		},
		"internalCPUSysctlbynameBytes": {
			linkname: "//go:linkname internalCPUSysctlbynameBytes internal/cpu.sysctlbynameBytes",
			params:   2, results: 1, indices: map[string]bool{"name": true, "out": true},
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
		indices := make(map[string]bool)
		callsSysctl := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.IndexExpr:
				owner, ownerOK := node.X.(*ast.Ident)
				index, indexOK := node.Index.(*ast.BasicLit)
				if ownerOK && indexOK && index.Kind == token.INT && index.Value == "0" {
					indices[owner.Name] = true
				}
			case *ast.CallExpr:
				selector, selectorOK := node.Fun.(*ast.SelectorExpr)
				if selectorOK {
					owner, ownerOK := selector.X.(*ast.Ident)
					callsSysctl = callsSysctl || ownerOK && owner.Name == "cos" && selector.Sel.Name == "Sysctlbyname"
				}
			}
			return true
		})
		for name := range expect.indices {
			if !indices[name] {
				t.Errorf("%s lacks direct %s[0] empty-slice precondition", function.Name.Name, name)
			}
		}
		if !callsSysctl {
			t.Errorf("%s does not call clite/os.Sysctlbyname", function.Name.Name)
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
