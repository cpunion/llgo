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
		timer      bool
		adapter    bool
		doorbellOK bool
	}{
		{name: "linux-amd64-llgo", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "linux-amd64-timer", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc", native: true, timer: true, doorbellOK: true},
		{name: "darwin-arm64-llgo", goos: "darwin", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "darwin-arm64-timer", goos: "darwin", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc", native: true, timer: true, doorbellOK: true},
		{name: "linux-386-pipe-only", goos: "linux", goarch: "386", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
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
			timer := slices.Contains(pkg.GoFiles, "coro_executor_driver_timer_llgo.go") &&
				slices.Contains(pkg.GoFiles, "coro_target_wait_timer_llgo.go") &&
				slices.Contains(pkg.GoFiles, "coro_timer_owner_llgo.go")
			legacyDriver := slices.Contains(pkg.GoFiles, "coro_executor_driver_legacy.go")
			pipeWait := slices.Contains(pkg.GoFiles, "coro_target_wait_pipe_llgo.go")
			fallback := slices.Contains(pkg.GoFiles, "coro_target_none.go")
			adapter := slices.Contains(pkg.GoFiles, "coro_target_test_adapter.go")
			if native != test.native || timer != test.timer || legacyDriver == test.timer || pipeWait != (test.native && !test.timer) ||
				adapter != test.adapter || fallback != (!test.native && !test.adapter) {
				t.Fatalf("GoFiles = %v, native=%t timer=%t legacy-driver=%t pipe-wait=%t adapter=%t fallback=%t", pkg.GoFiles, native, timer, legacyDriver, pipeWait, adapter, fallback)
			}
			const doorbell = "github.com/goplus/llgo/runtime/internal/corodoorbell"
			if imported := slices.Contains(pkg.Imports, doorbell); imported != test.doorbellOK {
				t.Fatalf("Imports = %v, doorbell=%t", pkg.Imports, imported)
			}
			const clock = "github.com/goplus/llgo/runtime/internal/coroclock"
			if imported := slices.Contains(pkg.Imports, clock); imported != test.timer {
				t.Fatalf("Imports = %v, clock=%t", pkg.Imports, imported)
			}
		})
	}
}
