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
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestCoroNativeTargetBuildSelection(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		goarch     string
		tags       string
		native     bool
		adapter    bool
		doorbellOK bool
	}{
		{name: "linux-amd64-llgo", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "darwin-arm64-llgo", goos: "darwin", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "named-linux-without-capability", goos: "linux", goarch: "arm64", tags: "llgo,llgo_coro,nogc,nintendoswitch"},
		{name: "host-go-fallback", goos: "linux", goarch: "amd64", tags: "llgo_coro,nogc"},
		{name: "js-wasm-fallback", goos: "js", goarch: "wasm", tags: "llgo,llgo_coro,nogc"},
		{name: "baremetal-fallback", goos: "linux", goarch: "arm", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc,baremetal,cortexm"},
		{name: "runtime-adapter-overrides-native", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc,coro_runtime_adapter_test", adapter: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-json", "-tags="+test.tags, "./internal/runtime")
			cmd.Env = append(os.Environ(), "GOOS="+test.goos, "GOARCH="+test.goarch, "CGO_ENABLED=0")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go list coroutine target: %v\n%s", err, output)
			}
			var pkg struct {
				GoFiles []string
				Imports []string
			}
			if err := json.Unmarshal(output, &pkg); err != nil {
				t.Fatalf("decode coroutine target package: %v", err)
			}
			native := slices.Contains(pkg.GoFiles, "coro_target_native_llgo.go")
			fallback := slices.Contains(pkg.GoFiles, "coro_target_none.go")
			adapter := slices.Contains(pkg.GoFiles, "coro_target_test_adapter.go")
			if native != test.native || adapter != test.adapter || fallback != (!test.native && !test.adapter) {
				t.Fatalf("GoFiles = %v, native=%t adapter=%t fallback=%t", pkg.GoFiles, native, adapter, fallback)
			}
			const doorbell = "github.com/goplus/llgo/runtime/internal/corodoorbell"
			if imported := slices.Contains(pkg.Imports, doorbell); imported != test.doorbellOK {
				t.Fatalf("Imports = %v, doorbell=%t", pkg.Imports, imported)
			}
		})
	}
}
