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
	"errors"
	"fmt"
	"go/types"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/crosscompile"
	"golang.org/x/tools/go/ssa"
)

func TestNativeCoroDoorbellRuntimeABISelection(t *testing.T) {
	tests := []struct {
		name string
		conf *Config
		want bool
	}{
		{name: "nil"},
		{name: "linux", conf: &Config{Goos: "linux"}, want: true},
		{name: "darwin", conf: &Config{Goos: "darwin"}, want: true},
		{name: "windows", conf: &Config{Goos: "windows"}},
		{name: "named-target", conf: &Config{Goos: "linux", Target: "rp2040"}},
		{name: "baremetal-comma", conf: &Config{Goos: "linux", Tags: "nogc,baremetal,cortexm"}},
		{name: "baremetal-space", conf: &Config{Goos: "linux", Tags: "nogc baremetal cortexm"}},
		{name: "explicit-host", conf: &Config{Goos: "linux", Tags: "llgo_coro_host"}},
		{name: "adapter-test", conf: &Config{Goos: "linux", Tags: "nogc,coro_runtime_adapter_test"}},
		{name: "adapter-test-go-build-flags-equals", conf: &Config{Goos: "linux", GoBuildFlags: []string{"-tags=coro_runtime_adapter_test"}}},
		{name: "adapter-test-go-build-flags-pair", conf: &Config{Goos: "linux", GoBuildFlags: []string{"-tags", "coro_runtime_adapter_test"}}},
		{name: "adapter-test-go-build-flags-double-dash", conf: &Config{Goos: "linux", GoBuildFlags: []string{"--tags=coro_runtime_adapter_test"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeCoroDoorbellRuntimeABI(test.conf); got != test.want {
				t.Fatalf("native coroutine doorbell selection = %t, want %t", got, test.want)
			}
		})
	}
}

func TestHostCoroPullRuntimeABISelection(t *testing.T) {
	tests := []struct {
		name string
		conf *Config
		want bool
	}{
		{name: "nil"},
		{name: "wasm-wasi", conf: &Config{Goos: "wasip1", Goarch: "wasm"}, want: true},
		{name: "wasm-js", conf: &Config{Goos: "js", Goarch: "wasm"}, want: true},
		{name: "wasm-unknown", conf: &Config{Goos: "unknown", Goarch: "wasm"}, want: true},
		{name: "named-wasip2", conf: &Config{Goos: "linux", Goarch: "arm", Target: "wasip2", resolvedTargetBuildTags: []string{"tinygo.wasm", "wasip2"}}, want: true},
		{name: "named-wasm-unknown", conf: &Config{Goos: "linux", Goarch: "arm", Target: "wasm-unknown", resolvedTargetBuildTags: []string{"tinygo.wasm", "wasm_unknown"}}, want: true},
		{name: "baremetal-config", conf: &Config{Goos: "linux", Goarch: "arm", Tags: "nogc,baremetal"}, want: true},
		{name: "baremetal-resolved-target", conf: &Config{Goos: "linux", Goarch: "arm", Target: "rp2040", resolvedTargetBuildTags: []string{"rp2040", "baremetal"}}, want: true},
		{name: "explicit-embedded", conf: &Config{Goos: "linux", Goarch: "arm64", Tags: "llgo_coro_host"}, want: true},
		{name: "explicit-embedded-go-flags", conf: &Config{Goos: "linux", Goarch: "arm64", GoBuildFlags: []string{"-tags=llgo_coro_host"}}, want: true},
		{name: "explicit-embedded-resolved-target", conf: &Config{Goos: "linux", Goarch: "arm64", Target: "board", resolvedTargetBuildTags: []string{"llgo_coro_host"}}, want: true},
		{name: "native-pipe", conf: &Config{Goos: "linux", Goarch: "amd64"}},
		{name: "unsupported-windows", conf: &Config{Goos: "windows", Goarch: "amd64"}},
		{name: "test-adapter-wasm", conf: &Config{Goos: "wasip1", Goarch: "wasm", Tags: "coro_runtime_adapter_test"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hostCoroPullRuntimeABI(test.conf); got != test.want {
				t.Fatalf("host coroutine pull selection = %t, want %t", got, test.want)
			}
			if got := nativeCoroDoorbellRuntimeABI(test.conf); got && test.want {
				t.Fatal("host-pull and native-pipe coroutine ABIs were both selected")
			}
		})
	}
}

func TestValidateCoroHostPullEntryConfigRejectsPythonOwnership(t *testing.T) {
	host := &Config{Goos: "wasip1", Goarch: "wasm"}
	if err := validateCoroHostPullEntryConfig(host, false); err != nil {
		t.Fatalf("host-pull entry without Python ownership: %v", err)
	}
	err := validateCoroHostPullEntryConfig(host, true)
	if err == nil {
		t.Fatal("host-pull entry accepted compiler-owned Python finalization")
	}
	for _, want := range []string{"host-pull", "Python", "Py_Finalize"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("host-pull Python error = %q, want %q", err, want)
		}
	}
	if err := validateCoroHostPullEntryConfig(
		&Config{Goos: "linux", Goarch: "amd64"}, true,
	); err != nil {
		t.Fatalf("native pipe entry unexpectedly rejected Python ownership: %v", err)
	}
}

func TestNativeCoroTimerRuntimeABISelection(t *testing.T) {
	tests := []struct {
		name string
		conf *Config
		want bool
	}{
		{name: "nil"},
		{name: "linux-amd64", conf: &Config{Goos: "linux", Goarch: "amd64"}, want: true},
		{name: "linux-arm64", conf: &Config{Goos: "linux", Goarch: "arm64"}, want: true},
		{name: "linux-loong64", conf: &Config{Goos: "linux", Goarch: "loong64"}, want: true},
		{name: "linux-ppc64", conf: &Config{Goos: "linux", Goarch: "ppc64"}, want: true},
		{name: "linux-ppc64le", conf: &Config{Goos: "linux", Goarch: "ppc64le"}, want: true},
		{name: "linux-riscv64", conf: &Config{Goos: "linux", Goarch: "riscv64"}, want: true},
		{name: "linux-s390x", conf: &Config{Goos: "linux", Goarch: "s390x"}, want: true},
		{name: "darwin-amd64", conf: &Config{Goos: "darwin", Goarch: "amd64"}, want: true},
		{name: "darwin-arm64", conf: &Config{Goos: "darwin", Goarch: "arm64"}, want: true},
		{name: "darwin-loong64-not-a-supported-clock-target", conf: &Config{Goos: "darwin", Goarch: "loong64"}},
		{name: "linux-386-unverified-time-abi", conf: &Config{Goos: "linux", Goarch: "386"}},
		{name: "linux-arm-unverified-time-abi", conf: &Config{Goos: "linux", Goarch: "arm"}},
		{name: "windows-amd64", conf: &Config{Goos: "windows", Goarch: "amd64"}},
		{name: "named-target", conf: &Config{Goos: "linux", Goarch: "arm64", Target: "nintendoswitch"}},
		{name: "baremetal", conf: &Config{Goos: "linux", Goarch: "arm64", Tags: "baremetal"}},
		{name: "adapter-test", conf: &Config{Goos: "linux", Goarch: "amd64", Tags: "coro_runtime_adapter_test"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeCoroTimerRuntimeABI(test.conf); got != test.want {
				t.Fatalf("native coroutine timer selection = %t, want %t", got, test.want)
			}
		})
	}
}

func TestEffectiveBuildTagsRejectsForgedNativeCapability(t *testing.T) {
	tests := []struct {
		name       string
		conf       *Config
		export     crosscompile.Export
		wantSource string
	}{
		{
			name:       "config-tags",
			conf:       &Config{Tags: "nogc," + coroNativePipeBuildTag},
			wantSource: "Config.Tags",
		},
		{
			name:       "go-build-flags-equals",
			conf:       &Config{GoBuildFlags: []string{"-tags=nogc," + coroNativePipeBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name:       "go-build-flags-pair",
			conf:       &Config{GoBuildFlags: []string{"-tags", "nogc " + coroNativePipeBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name:       "go-build-flags-double-dash-equals",
			conf:       &Config{GoBuildFlags: []string{"--tags=nogc," + coroNativePipeBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name:       "go-build-flags-double-dash-pair",
			conf:       &Config{GoBuildFlags: []string{"--tags", "nogc " + coroNativePipeBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name:       "named-target-build-tags",
			conf:       &Config{Goos: "linux", Target: "nintendoswitch"},
			export:     crosscompile.Export{BuildTags: []string{"nintendoswitch", coroNativePipeBuildTag}},
			wantSource: "named-target BuildTags",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := effectiveBuildTags(test.conf, test.export)
			if err == nil {
				t.Fatal("forged native capability was accepted")
			}
			for _, want := range []string{coroNativePipeBuildTag, test.wantSource, "compiler-reserved capability"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestDoRejectsForgedNativeCapabilityBeforePackageSelection(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.Tags = "nogc," + coroNativePipeBuildTag
	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if err == nil {
		t.Fatal("Do accepted a forged native capability")
	}
	for _, want := range []string{coroNativePipeBuildTag, "Config.Tags", "compiler-reserved capability"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Do error = %q, want %q", err, want)
		}
	}
}

func TestEffectiveBuildTagsSelectsUniqueNativeFleet(t *testing.T) {
	conf := &Config{
		Goos:   "linux",
		Goarch: "amd64"}
	tags, err := effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tags, "llgo_coro_native_fleet") || !nativeCoroFleetRuntimeABI(conf) {
		t.Fatalf("configured native fleet tags/runtime = %q/%t", tags, nativeCoroFleetRuntimeABI(conf))
	}
}

func TestEffectiveBuildTagsRejectsForgedNativeTimerCapability(t *testing.T) {
	tests := []struct {
		name       string
		conf       *Config
		export     crosscompile.Export
		wantSource string
	}{
		{
			name:       "config-tags",
			conf:       &Config{Tags: "nogc," + coroNativeTimerBuildTag},
			wantSource: "Config.Tags",
		},
		{
			name:       "go-build-flags-equals",
			conf:       &Config{GoBuildFlags: []string{"-tags=nogc," + coroNativeTimerBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name:       "go-build-flags-pair",
			conf:       &Config{GoBuildFlags: []string{"--tags", "nogc " + coroNativeTimerBuildTag}},
			wantSource: "Config.GoBuildFlags",
		},
		{
			name: "named-target-build-tags",
			conf: &Config{
				Goos: "linux", Goarch: "arm64", Target: "nintendoswitch"},
			export:     crosscompile.Export{BuildTags: []string{"nintendoswitch", coroNativeTimerBuildTag}},
			wantSource: "named-target BuildTags",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := effectiveBuildTags(test.conf, test.export)
			if err == nil {
				t.Fatal("forged native timer capability was accepted")
			}
			for _, want := range []string{coroNativeTimerBuildTag, test.wantSource, "compiler-reserved capability"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestEffectiveBuildTagsKeepsNativeTimerCapabilityCompilerOwned(t *testing.T) {
	conf := &Config{Goos: "linux", Goarch: "amd64"}
	tags, err := effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	effective := strings.Split(tags, ",")
	for _, want := range []string{coroNativePipeBuildTag, coroNativeTimerBuildTag} {
		if !slices.Contains(effective, want) {
			t.Fatalf("effective tags = %q, missing %q", tags, want)
		}
	}

	conf.Goarch = "arm"
	tags, err = effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	effective = strings.Split(tags, ",")
	if !slices.Contains(effective, coroNativePipeBuildTag) || slices.Contains(effective, coroNativeTimerBuildTag) {
		t.Fatalf("32-bit effective tags = %q, want pipe without unverified timer", tags)
	}
}

func TestEffectiveBuildTagsKeepsNativeCapabilityCompilerOwned(t *testing.T) {
	tests := []struct {
		name string
		conf *Config
		want bool
	}{
		{
			name: "default-linux-program-bootstrap",
			conf: &Config{Goos: "linux"},
			want: true,
		},
		{
			name: "named-linux-target",
			conf: &Config{Goos: "linux", Target: "nintendoswitch"},
		},
		{
			name: "runtime-adapter",
			conf: &Config{Goos: "linux", Tags: "coro_runtime_adapter_test"},
		},
		{
			name: "isolated-runtime-compiler-channel",
			conf: &Config{Goos: "linux", compilerBuildTags: []string{"llgo_coro", coroNativePipeBuildTag}},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tags, err := effectiveBuildTags(test.conf, crosscompile.Export{})
			if err != nil {
				t.Fatal(err)
			}
			got := slices.Contains(strings.Split(tags, ","), coroNativePipeBuildTag)
			if got != test.want {
				t.Fatalf("effective tags = %q, native capability=%t, want %t", tags, got, test.want)
			}
		})
	}
}

func TestEffectiveBuildTagsDoesNotMisreadOrdinaryFlagValues(t *testing.T) {
	conf := &Config{GoBuildFlags: []string{
		"-gcflags=-tags=" + coroNativePipeBuildTag,
		"-ldflags=-X=main.tag=" + coroNativePipeBuildTag,
	}}
	if _, err := effectiveBuildTags(conf, crosscompile.Export{}); err != nil {
		t.Fatalf("ordinary non-tag flag value was rejected: %v", err)
	}
}

func TestEffectiveBuildTagsMergesGoBuildFlagTags(t *testing.T) {
	conf := &Config{
		Goos: "linux",

		GoBuildFlags: []string{
			"-mod=mod",
			"-tags=user_feature_a",
			"-gcflags=-N",
			"-tags", "user_feature_b user_feature_c",
			"--tags=user_feature_d",
		}}
	tags, err := effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	effective := strings.Split(tags, ",")
	for _, want := range []string{"llgo", "llgo_coro", coroNativePipeBuildTag, "user_feature_a", "user_feature_b", "user_feature_c", "user_feature_d"} {
		if !slices.Contains(effective, want) {
			t.Fatalf("effective tags = %q, missing %q", tags, want)
		}
	}
	_, other := partitionGoBuildFlags(conf.GoBuildFlags)
	if want := []string{"-mod=mod", "-gcflags=-N"}; !slices.Equal(other, want) {
		t.Fatalf("non-tag GoBuildFlags = %v, want %v", other, want)
	}
}

func TestRealNativeCoroTargetIsTrustedPlainSchedulerIsland(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine target plan requires Darwin or Linux")
	}
	sentinel := errors.New("native target plan verified")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			return nil, err
		}
		raw, err := input.liveCoroRawABIPlainClosure(plan, nil)
		if err != nil {
			return nil, err
		}
		useDomains, err := inspectCoroForeignUseDomains(input, plan, raw, true)
		if err != nil {
			return nil, err
		}
		if !useDomains.Closed {
			return nil, fmt.Errorf("native target foreign use-domain report is not closed")
		}
		// This target builds cl/_testgo/print, so only declarations in that
		// program's exact emission universe may be asserted here. The producer
		// proof test separately covers every target-selected poll/signal C leaf,
		// including declarations that this program does not reach.
		expectedLLVMInferred := map[string]bool{
			"__llgo_runtime_poll_desc_publish_operation_v1": false,
			"__llgo_runtime_poll_desc_clear_operation_v1":   false,
			"__llgo_runtime_poll_desc_load_operation_v1":    false,
			"__llgo_runtime_poll_desc_deadline_v1":          false,
			"__llgo_coro_worker_queue_can_release_v1":       false,
		}
		for _, record := range useDomains.certificateRecords() {
			t.Log(record.diagnostic())
		}
		for _, record := range useDomains.Records {
			if _, expected := expectedLLVMInferred[record.PhysicalSymbol]; !expected {
				continue
			}
			certificate, inferred := plan.CallableContractCertificate(
				record.Function,
			)
			function, planned := plan.FunctionPlan(record.Function)
			if !record.DirectExecutorCertified ||
				record.NoBlockCertified ||
				record.SyncCertified ||
				!inferred ||
				!strings.HasPrefix(
					string(certificate.Contract.ID),
					"llvm-executor-leaf.v1/",
				) ||
				!planned ||
				function.External != coro.ExternalKnown ||
				function.Effect != coro.NoSuspend ||
				function.Exec != coro.IRQUnsafe {
				return nil, fmt.Errorf(
					"native target inferred foreign declaration %q has no exact executor-leaf plan: "+
						"record=%s certificate=%+v inferred=%t function=%+v planned=%t",
					record.PhysicalSymbol, record.diagnostic(),
					certificate, inferred, function, planned,
				)
			}
			t.Log("inferred LLVM executor leaf " + record.diagnostic())
		}
		for _, function := range input.EmissionUniverse.Functions() {
			identity, external, err := input.callableIdentity(function)
			if err != nil {
				return nil, fmt.Errorf(
					"native target callable identity for %q: %w",
					function.Name(), err,
				)
			}
			if !external {
				continue
			}
			if _, expected := expectedLLVMInferred[identity.PhysicalSymbol]; !expected {
				continue
			}
			certificate, inferred, err :=
				input.callableContract(function)
			if err != nil {
				return nil, fmt.Errorf(
					"native target inferred foreign declaration %q: %w",
					identity.PhysicalSymbol, err,
				)
			}
			if !inferred || certificate.ID == "" ||
				certificate.PhysicalSymbol != identity.PhysicalSymbol ||
				!coro.CallableContractDirectExecutorCompatible(
					certificate.Contract,
				) {
				return nil, fmt.Errorf(
					"native target inferred foreign declaration %q has no exact frontend certificate: "+
						"certificate=%+v inferred=%t",
					identity.PhysicalSymbol, certificate, inferred,
				)
			}
			expectedLLVMInferred[identity.PhysicalSymbol] = true
		}
		for symbol, found := range expectedLLVMInferred {
			if !found {
				return nil, fmt.Errorf(
					"native target inferred foreign declaration %q is absent from the exact emission universe",
					symbol,
				)
			}
		}
		const runtimePath = "github.com/goplus/llgo/runtime/internal/runtime"
		const clitePath = "github.com/goplus/llgo/runtime/internal/clite"
		validateMigratedRawHost := func(record coroForeignUseDomainRecord, identity string) error {
			if record.DirectExecutorCertified ||
				record.NoBlockCertified ||
				record.SyncCertified ||
				!record.rawHostOnly() {
				return fmt.Errorf(
					"native target migrated raw-host declaration %q = %s",
					identity, record.diagnostic(),
				)
			}
			if _, legacy := plan.ForeignNoBlockCertificate(record.Function); legacy {
				return fmt.Errorf(
					"native target raw-host declaration %q retained a foreign-noblock certificate",
					identity,
				)
			}
			if _, legacy := plan.ForeignSyncCertificate(record.Function); legacy {
				return fmt.Errorf(
					"native target raw-host declaration %q retained a foreign-sync certificate",
					identity,
				)
			}
			callable, certified := plan.CallableContractCertificate(record.Function)
			function, planned := plan.FunctionPlan(record.Function)
			if !certified ||
				callable.Scope != coro.CallableContractScopeDeclaration ||
				!coroRawPlainDirectForeignContractCompatible(callable.Contract) ||
				!planned ||
				function.External != coro.ExternalUnknownForeign ||
				function.ManagedDemand != coro.NoDemand ||
				!function.RawPlainDemand ||
				function.Emission != coro.EmitExternal ||
				!function.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) {
				return fmt.Errorf(
					"native target raw-host declaration %q lost its conservative default: "+
						"callable=%+v certified=%t function=%+v planned=%t",
					identity, callable, certified, function, planned,
				)
			}
			return nil
		}
		validateElidedTypedControl := func(record coroForeignUseDomainRecord, identity string) error {
			if record.DirectExecutorCertified ||
				record.NoBlockCertified ||
				record.SyncCertified ||
				record.Calls != 0 ||
				record.RawHostCalls != 0 ||
				!slices.Equal(record.Rejections, []string{"no-live-call"}) {
				return fmt.Errorf(
					"native target elided typed-control declaration %q retained a foreign use-domain: %s",
					identity, record.diagnostic(),
				)
			}
			if _, legacy := plan.ForeignNoBlockCertificate(record.Function); legacy {
				return fmt.Errorf(
					"native target elided typed-control declaration %q retained a foreign-noblock certificate",
					identity,
				)
			}
			if _, legacy := plan.ForeignSyncCertificate(record.Function); legacy {
				return fmt.Errorf(
					"native target elided typed-control declaration %q retained a foreign-sync certificate",
					identity,
				)
			}
			callable, certified := plan.CallableContractCertificate(record.Function)
			if !certified ||
				callable.Scope != coro.CallableContractScopeDeclaration ||
				!coroRawPlainDirectForeignContractCompatible(callable.Contract) {
				return fmt.Errorf(
					"native target elided typed-control declaration %q lost its conservative unused declaration contract: "+
						"callable=%+v certified=%t",
					identity, callable, certified,
				)
			}
			function, planned := plan.FunctionPlan(record.Function)
			if !planned ||
				function.External != coro.ExternalUnknownForeign ||
				function.ManagedDemand != coro.NoDemand ||
				function.RawPlainDemand ||
				function.Emission != coro.EmitNone {
				return fmt.Errorf(
					"native target elided typed-control declaration %q retained a physical foreign target: "+
						"function=%+v planned=%t",
					identity, function, planned,
				)
			}
			return nil
		}
		migratedRawHostSymbols := map[string]bool{
			"__llgo_coro_fleet_owner_count_v1":               false,
			"__llgo_coro_fleet_factory_start_v1":             false,
			"__llgo_coro_fleet_owner_create_v3":              false,
			"__llgo_coro_fleet_owner_stop_standby_v1":        false,
			"__llgo_coro_fleet_factory_stop_v2":              false,
			"__llgo_coro_worker_create_v1":                   false,
			"__llgo_coro_worker_queue_init_v1":               false,
			"__llgo_coro_worker_queue_stop_v1":               false,
			"__llgo_coro_worker_queue_destroy_after_join_v1": false,
			"__llgo_coro_worker_call_v1":                     false,
			"__llgo_coro_worker_queue_reserve_v1":            false,
			"__llgo_coro_worker_queue_cancel_reservation_v1": false,
			"__llgo_coro_worker_queue_submit_reserved_v1":    false,
			"__llgo_coro_fleet_owner_retire_self_v1":         false,
			"__llgo_coro_doorbell_open_v1":                   false,
			"__llgo_coro_doorbell_read_v1":                   false,
			"__llgo_coro_doorbell_write_v1":                  false,
			"__llgo_coro_doorbell_close_v1":                  false,
			"GC_init":                                        false,
			"GC_add_roots":                                   false,
			"pthread_self":                                   false,
		}
		elidedTypedControlSymbols := map[string]bool{
			"siglongjmp": false,
		}
		if runtime.GOOS == "linux" {
			elidedTypedControlSymbols["__sigsetjmp"] = false
		} else {
			elidedTypedControlSymbols["sigsetjmp"] = false
		}
		if runtime.GOOS == "darwin" {
			migratedRawHostSymbols["clock_gettime_nsec_np"] = false
		}
		for _, record := range useDomains.Records {
			if _, typedControl := elidedTypedControlSymbols[record.PhysicalSymbol]; typedControl {
				if elidedTypedControlSymbols[record.PhysicalSymbol] {
					return nil, fmt.Errorf("native target typed-control declaration %q is ambiguous", record.PhysicalSymbol)
				}
				elidedTypedControlSymbols[record.PhysicalSymbol] = true
				if err := validateElidedTypedControl(record, record.PhysicalSymbol); err != nil {
					return nil, err
				}
				t.Log("elided typed control " + record.diagnostic())
				continue
			}
			if _, migrated := migratedRawHostSymbols[record.PhysicalSymbol]; !migrated {
				continue
			}
			if migratedRawHostSymbols[record.PhysicalSymbol] {
				return nil, fmt.Errorf("native target raw-host declaration %q is ambiguous", record.PhysicalSymbol)
			}
			migratedRawHostSymbols[record.PhysicalSymbol] = true
			if err := validateMigratedRawHost(record, record.PhysicalSymbol); err != nil {
				return nil, err
			}
			t.Log("migrated " + record.diagnostic())
		}
		for symbol, found := range migratedRawHostSymbols {
			if !found {
				return nil, fmt.Errorf("native target raw-host declaration %q is absent from the closed report", symbol)
			}
		}
		for symbol, found := range elidedTypedControlSymbols {
			if !found {
				return nil, fmt.Errorf("native target typed-control declaration %q is absent from the closed report", symbol)
			}
		}
		terminalDeclarations := map[string]string{
			runtimePath + ".coroTerminalFputs": "fputs",
			runtimePath + ".coroTerminalFputc": "fputc",
		}
		ordinaryFputcFound := false
		for _, record := range useDomains.Records {
			identity := record.Function.String()
			if physical, terminal := terminalDeclarations[identity]; terminal {
				if record.PhysicalSymbol != physical {
					return nil, fmt.Errorf(
						"native target terminal declaration %q uses %q, want %q",
						identity, record.PhysicalSymbol, physical,
					)
				}
				if err := validateMigratedRawHost(record, identity); err != nil {
					return nil, err
				}
				delete(terminalDeclarations, identity)
				t.Log("migrated terminal " + record.diagnostic())
				continue
			}
			if identity != clitePath+".Fputc" {
				continue
			}
			if ordinaryFputcFound {
				return nil, fmt.Errorf("native target ordinary Fputc declaration is ambiguous")
			}
			ordinaryFputcFound = true
			if record.PhysicalSymbol != "fputc" ||
				record.DirectExecutorCertified ||
				record.SyncCertified ||
				record.rawHostOnly() ||
				!record.defaultUseDomainCompatible() ||
				record.RawHostCalls != 0 ||
				!slices.Contains(record.Rejections, "managed-call") {
				return nil, fmt.Errorf(
					"native target ordinary Fputc inherited terminal raw-host authority: %s",
					record.diagnostic(),
				)
			}
			if _, legacy := plan.ForeignSyncCertificate(record.Function); legacy {
				return nil, fmt.Errorf("native target ordinary Fputc acquired a foreign-sync certificate")
			}
		}
		for identity := range terminalDeclarations {
			return nil, fmt.Errorf(
				"native target terminal declaration %q is absent from the closed report",
				identity,
			)
		}
		if !ordinaryFputcFound {
			return nil, fmt.Errorf("native target ordinary Fputc declaration is absent from the closed report")
		}
		find := func(path, name string) (*ssa.Function, error) {
			var found *ssa.Function
			for _, pkg := range input.Program.AllPackages() {
				if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Path() != path {
					continue
				}
				for _, member := range pkg.Members {
					function, ok := member.(*ssa.Function)
					if !ok || function.Name() != name {
						continue
					}
					if found != nil && found != function {
						return nil, fmt.Errorf("native target function %s.%s is ambiguous", path, name)
					}
					found = function
				}
			}
			if found == nil {
				return nil, fmt.Errorf("native target function %s.%s is absent", path, name)
			}
			return found, nil
		}
		const doorbellPath = "github.com/goplus/llgo/runtime/internal/corodoorbell"
		for _, want := range []struct {
			path     string
			name     string
			external bool
		}{
			{path: runtimePath, name: "coroTargetExecutorStartV1"},
			{path: runtimePath, name: "coroTargetBeginExecutorWaitV1"},
			{path: runtimePath, name: "coroTargetBeginExecutorCloseV1"},
			{path: runtimePath, name: coroKeyedParkSymbolV2},
			{path: runtimePath, name: coroKeyedResumeSymbolV2},
			{path: runtimePath, name: coroSemaphorePrepareOrAbortSymbolV2},
			{path: runtimePath, name: coroSemaphoreReleaseOrAbortSymbolV2},
			{path: runtimePath, name: coroNotifyPrepareOrAbortSymbolV2},
			{path: runtimePath, name: coroNotifyOneOrAbortSymbolV2},
			{path: runtimePath, name: coroNotifyAllOrAbortSymbolV2},
			{path: doorbellPath, name: "nativePipeOpen"},
			{path: doorbellPath, name: "nativePipeRead"},
			{path: doorbellPath, name: "nativePipeWrite"},
			{path: doorbellPath, name: "nativePipePoll"},
			{path: doorbellPath, name: "nativeCPoll", external: true},
		} {
			function, err := find(want.path, want.name)
			if err != nil {
				return nil, err
			}
			if _, required := input.requiredPlain[function]; !required && !want.external {
				return nil, fmt.Errorf("native target function %s.%s is outside required plain closure", want.path, want.name)
			}
			if want.name == "nativeCPoll" {
				nfdsType := types.Type(types.Typ[types.Uintptr])
				if runtime.GOOS == "darwin" {
					nfdsType = types.Typ[types.Uint32]
				}
				if function.Signature.Params().Len() != 3 ||
					!types.Identical(function.Signature.Params().At(1).Type(), nfdsType) {
					return nil, fmt.Errorf("native poll nfds parameter = %s, want exact %s on %s",
						function.Signature, nfdsType, runtime.GOOS)
				}
			}
			functionPlan, ok := plan.FunctionPlan(function)
			if !ok || functionPlan.Exec.Contains(coro.NeedsPreempt) || !want.external && functionPlan.Exec.Contains(coro.BlockForeign) {
				return nil, fmt.Errorf("native target function %s.%s plan = %+v, present=%t", want.path, want.name, functionPlan, ok)
			}
			if want.external {
				callable, certified := plan.CallableContractCertificate(function)
				_, declarationTagged := input.requiredPlain[function]
				if !certified || callable.Scope != coro.CallableContractScopeDeclaration ||
					!coroRawPlainDirectForeignContractCompatible(callable.Contract) || declarationTagged ||
					functionPlan.External != coro.ExternalUnknownForeign || functionPlan.Emission != coro.EmitExternal ||
					functionPlan.ManagedDemand != coro.NoDemand || !functionPlan.RawPlainDemand ||
					!functionPlan.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) {
					return nil, fmt.Errorf("native poll leaf plan = %+v, callable=%+v certified=%t declaration-tagged=%t; want occurrence-inferred exact raw-host external",
						functionPlan, callable, certified, declarationTagged)
				}
			} else if functionPlan.External != coro.Defined || functionPlan.ManagedDemand != coro.NoDemand ||
				!functionPlan.RawPlainDemand || !functionPlan.RawPlainOnly || functionPlan.Emission != coro.EmitRawPlain ||
				functionPlan.Primary != coro.PrimaryPlain || functionPlan.FuncRep != coro.DirectPlain {
				return nil, fmt.Errorf("native target body %s.%s plan = %+v, want direct raw-only plain", want.path, want.name, functionPlan)
			}
		}
		return nil, sentinel
	}
	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want verified native target plan", err)
	}
}
