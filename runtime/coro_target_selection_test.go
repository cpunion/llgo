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
	"strings"
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
		host       bool
		profile    string
		adapter    bool
		doorbellOK bool
	}{
		{name: "linux-amd64-llgo", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "linux-amd64-timer", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc", native: true, timer: true, doorbellOK: true},
		{name: "darwin-arm64-llgo", goos: "darwin", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "darwin-arm64-timer", goos: "darwin", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc", native: true, timer: true, doorbellOK: true},
		{name: "linux-386-pipe-only", goos: "linux", goarch: "386", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc", native: true, doorbellOK: true},
		{name: "named-linux-without-capability", goos: "linux", goarch: "arm64", tags: "llgo,llgo_coro,nogc,nintendoswitch"},
		{name: "host-go-fallback", goos: "linux", goarch: "amd64", tags: "llgo_coro,nogc", doorbellOK: true},
		{name: "js-wasm-host", goos: "js", goarch: "wasm", tags: "llgo,llgo_coro,nogc", host: true, profile: "coro_target_host_profile_js_llgo.go"},
		{name: "wasip1-host", goos: "wasip1", goarch: "wasm", tags: "llgo,llgo_coro,nogc", host: true, profile: "coro_target_host_profile_wasi_llgo.go"},
		{name: "baremetal-host", goos: "linux", goarch: "arm", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc,baremetal,cortexm", host: true, profile: "coro_target_host_profile_baremetal_llgo.go"},
		{name: "explicit-embedded-host", goos: "linux", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_host,nogc,nintendoswitch", host: true, profile: "coro_target_host_profile_embedded_llgo.go"},
		{name: "runtime-adapter-overrides-native", goos: "linux", goarch: "amd64", tags: "llgo,llgo_coro,llgo_coro_native_pipe,nogc,coro_runtime_adapter_test", adapter: true, doorbellOK: true},
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
			host := slices.Contains(pkg.GoFiles, "coro_target_host_llgo.go") &&
				slices.Contains(pkg.GoFiles, "coro_executor_driver_host_llgo.go")
			profileCount := 0
			for _, name := range pkg.GoFiles {
				if strings.HasPrefix(name, "coro_target_host_profile_") {
					profileCount++
				}
			}
			pipeWait := slices.Contains(pkg.GoFiles, "coro_target_wait_pipe_llgo.go")
			fallback := slices.Contains(pkg.GoFiles, "coro_target_none.go")
			adapter := slices.Contains(pkg.GoFiles, "coro_target_test_adapter.go")
			if native != test.native || timer != test.timer || host != test.host ||
				(profileCount == 1) != test.host || test.profile != "" && !slices.Contains(pkg.GoFiles, test.profile) ||
				legacyDriver != (!test.timer && !test.host) || pipeWait != (test.native && !test.timer) ||
				adapter != test.adapter || fallback != (!test.native && !test.host && !test.adapter) {
				t.Fatalf("GoFiles = %v, native=%t timer=%t host=%t legacy-driver=%t pipe-wait=%t adapter=%t fallback=%t", pkg.GoFiles, native, timer, host, legacyDriver, pipeWait, adapter, fallback)
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

func TestCoroNativeFleetTargetBuildSelection(t *testing.T) {
	const tags = "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,llgo_coro_native_fleet,nogc"
	cmd := exec.Command("go", "list", "-json", "-tags="+tags, "./internal/runtime")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list native coroutine fleet target: %v\n%s", err, output)
	}
	var pkg struct {
		GoFiles []string
		Imports []string
	}
	if err := json.Unmarshal(output, &pkg); err != nil {
		t.Fatal("decode native coroutine fleet target:", err)
	}
	for _, required := range []string{
		"coro_target_native_fleet_llgo.go",
		"coro_native_fleet_owner_llgo.go",
		"coro_native_fleet_program_llgo.go",
		"coro_native_fleet_reactor.go",
		"coro_ready_distribution_fleet_llgo.go",
		"coro_worker_completion_fleet_llgo.go",
	} {
		if !slices.Contains(pkg.GoFiles, required) {
			t.Errorf("native fleet GoFiles lack %s: %v", required, pkg.GoFiles)
		}
	}
	for _, forbidden := range []string{
		"coro_target_native_llgo.go",
		"coro_ready_distribution_default.go",
		"coro_target_executor_retired_default.go",
		"coro_worker_completion_program_llgo.go",
		"coro_target_none.go",
	} {
		if slices.Contains(pkg.GoFiles, forbidden) {
			t.Errorf("native fleet GoFiles unexpectedly contain %s: %v", forbidden, pkg.GoFiles)
		}
	}
	if !slices.Contains(pkg.Imports, "github.com/goplus/llgo/runtime/internal/corofleet") {
		t.Fatalf("native fleet runtime imports lack fixed pthread owner adapter: %v", pkg.Imports)
	}

	cmd = exec.Command("go", "list", "-json", "-tags="+tags, "./internal/corofleet")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list fixed coroutine fleet owner adapter: %v\n%s", err, output)
	}
	var owner struct {
		GoFiles []string
	}
	if err := json.Unmarshal(output, &owner); err != nil {
		t.Fatal("decode fixed coroutine fleet owner adapter:", err)
	}
	if !slices.Contains(owner.GoFiles, "call_llgo.go") ||
		!slices.Contains(owner.GoFiles, "build_nogc_llgo.go") ||
		slices.Contains(owner.GoFiles, "build_gc_llgo.go") {
		t.Fatalf("fixed coroutine fleet owner GoFiles = %v", owner.GoFiles)
	}
}

func TestCoroHostTargetCrossCompile(t *testing.T) {
	tests := []struct {
		name, goos, goarch, tags string
	}{
		{name: "js-wasm", goos: "js", goarch: "wasm", tags: "llgo,llgo_coro,nogc"},
		{name: "wasip1", goos: "wasip1", goarch: "wasm", tags: "llgo,llgo_coro,nogc"},
		{name: "baremetal", goos: "linux", goarch: "arm", tags: "llgo,llgo_coro,nogc,baremetal,cortexm"},
		{name: "embedded", goos: "linux", goarch: "arm64", tags: "llgo,llgo_coro,llgo_coro_host,nogc,nintendoswitch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("go", "build", "-tags="+test.tags, "./internal/runtime")
			cmd.Env = append(os.Environ(), "GOOS="+test.goos, "GOARCH="+test.goarch, "CGO_ENABLED=0")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("cross compile host coroutine target: %v\n%s", err, output)
			}
		})
	}
}

func TestCoroHostTargetOwnedReactorSourceContract(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile("internal/runtime/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	target := read("coro_target_host_llgo.go")
	driver := read("coro_executor_driver_host_llgo.go")
	for _, required := range []string{
		"coroProgramDriverModeV2State == coroProgramDriverModeSliceV2",
		"return coroTargetRunRequestQueuedV2",
		"func __llgo_coro_host_next_action_v1",
		"func __llgo_coro_host_next_deadline_v1",
		"func __llgo_coro_host_continue_slice_v1",
	} {
		if !strings.Contains(target, required) {
			t.Errorf("host target lacks owned-reactor contract %q", required)
		}
	}
	for _, forbidden := range []string{"internal/clite/pthread", "internal/clite/libuv", "internal/clite/bdwgc", "internal/corodoorbell"} {
		if strings.Contains(target, forbidden) || strings.Contains(driver, forbidden) {
			t.Errorf("host adapter imports forbidden backend %q", forbidden)
		}
	}
	if strings.Contains(driver, "Poll:") || strings.Contains(driver, "Worker:") {
		t.Error("host source catalog pretends to provide Poll or Worker")
	}
	js := read("coro_target_host_profile_js_llgo.go")
	wasi := read("coro_target_host_profile_wasi_llgo.go")
	if !strings.Contains(js, "microtask") || !strings.Contains(js, "timeout") ||
		strings.Contains(js, "coroHostCapabilityReactorPollV1") {
		t.Error("JS profile does not freeze nonblocking microtask/timeout capabilities")
	}
	if !strings.Contains(wasi, "poll_oneoff") || !strings.Contains(wasi, "coroHostCapabilityReactorPollV1") {
		t.Error("WASI profile does not freeze its host-owned reactor poll capability")
	}
}
