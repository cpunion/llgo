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

package coroalloc

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestWebAssemblyTargetsSelectExplicitCollectorBackend(t *testing.T) {
	targets := []struct {
		name   string
		goos   string
		goarch string
		tags   string
	}{
		{name: "js-wasm", goos: "js", goarch: "wasm", tags: "llgo,tinygo.wasm"},
		{name: "wasip1", goos: "wasip1", goarch: "wasm", tags: "llgo,tinygo.wasm"},
		{name: "wasip2", goos: "linux", goarch: "arm", tags: "llgo,tinygo.wasm,wasip2"},
		{name: "wasm-unknown", goos: "linux", goarch: "arm", tags: "llgo,tinygo.wasm,wasm_unknown"},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command("go", "list", "-json", "-tags="+target.tags, ".")
			cmd.Env = append(os.Environ(),
				"GOOS="+target.goos,
				"GOARCH="+target.goarch,
				"CGO_ENABLED=0",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go list target package: %v\n%s", err, output)
			}

			var pkg struct {
				GoFiles     []string
				TestGoFiles []string
				Imports     []string
			}
			if err := json.Unmarshal(output, &pkg); err != nil {
				t.Fatalf("decode go list output: %v\n%s", err, output)
			}
			if !slices.Contains(pkg.GoFiles, "backend_webassembly.go") {
				t.Fatalf("GoFiles = %v, want backend_webassembly.go without the compiler GC capability", pkg.GoFiles)
			}
			if slices.Contains(pkg.GoFiles, "backend_gc.go") {
				t.Fatalf("GoFiles = %v, unexpectedly selected BDWGC backend", pkg.GoFiles)
			}
			if !slices.Contains(pkg.TestGoFiles, "backend_webassembly_test.go") {
				t.Fatalf("TestGoFiles = %v, want backend_webassembly_test.go", pkg.TestGoFiles)
			}
			if slices.Contains(pkg.TestGoFiles, "backend_gc_test.go") {
				t.Fatalf("TestGoFiles = %v, unexpectedly selected BDWGC backend test", pkg.TestGoFiles)
			}
			if slices.Contains(pkg.Imports, "github.com/goplus/llgo/runtime/internal/clite/bdwgc") {
				t.Fatalf("Imports = %v, unexpectedly retained BDWGC", pkg.Imports)
			}
		})
	}

	for _, target := range targets {
		t.Run(target.name+"-gc", func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command("go", "list", "-json", "-tags="+target.tags+",llgo_wasm_gc", ".")
			cmd.Env = append(os.Environ(),
				"GOOS="+target.goos,
				"GOARCH="+target.goarch,
				"CGO_ENABLED=0",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go list target GC package: %v\n%s", err, output)
			}

			var pkg struct {
				GoFiles []string
				Imports []string
			}
			if err := json.Unmarshal(output, &pkg); err != nil {
				t.Fatalf("decode go list output: %v\n%s", err, output)
			}
			if !slices.Contains(pkg.GoFiles, "backend_tinygogc.go") ||
				slices.Contains(pkg.GoFiles, "backend_webassembly.go") ||
				slices.Contains(pkg.GoFiles, "backend_gc.go") {
				t.Fatalf("GoFiles = %v, want only the tinygogc WebAssembly backend", pkg.GoFiles)
			}
			if !slices.Contains(pkg.Imports, "github.com/goplus/llgo/runtime/internal/runtime/tinygogc") {
				t.Fatalf("Imports = %v, want tinygogc", pkg.Imports)
			}
			if slices.Contains(pkg.Imports, "github.com/goplus/llgo/runtime/internal/clite/bdwgc") {
				t.Fatalf("Imports = %v, unexpectedly retained BDWGC", pkg.Imports)
			}
		})
	}
}
