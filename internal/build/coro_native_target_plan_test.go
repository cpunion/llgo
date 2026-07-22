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
		{name: "disabled", conf: &Config{Goos: "linux"}},
		{name: "linux", conf: &Config{Goos: "linux", CoroProfile: CoroProfileStackless}, want: true},
		{name: "darwin", conf: &Config{Goos: "darwin", CoroProfile: CoroProfileStackless}, want: true},
		{name: "windows", conf: &Config{Goos: "windows", CoroProfile: CoroProfileStackless}},
		{name: "named-target", conf: &Config{Goos: "linux", Target: "rp2040", CoroProfile: CoroProfileStackless}},
		{name: "baremetal-comma", conf: &Config{Goos: "linux", Tags: "nogc,baremetal,cortexm", CoroProfile: CoroProfileStackless}},
		{name: "baremetal-space", conf: &Config{Goos: "linux", Tags: "nogc baremetal cortexm", CoroProfile: CoroProfileStackless}},
		{name: "explicit-host", conf: &Config{Goos: "linux", Tags: "llgo_coro_host", CoroProfile: CoroProfileStackless}},
		{name: "adapter-test", conf: &Config{Goos: "linux", Tags: "nogc,coro_runtime_adapter_test", CoroProfile: CoroProfileStackless}},
		{name: "adapter-test-go-build-flags-equals", conf: &Config{Goos: "linux", GoBuildFlags: []string{"-tags=coro_runtime_adapter_test"}, CoroProfile: CoroProfileStackless}},
		{name: "adapter-test-go-build-flags-pair", conf: &Config{Goos: "linux", GoBuildFlags: []string{"-tags", "coro_runtime_adapter_test"}, CoroProfile: CoroProfileStackless}},
		{name: "adapter-test-go-build-flags-double-dash", conf: &Config{Goos: "linux", GoBuildFlags: []string{"--tags=coro_runtime_adapter_test"}, CoroProfile: CoroProfileStackless}},
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
		{name: "disabled-wasm", conf: &Config{Goos: "wasip1", Goarch: "wasm"}},
		{name: "wasm-wasi", conf: &Config{Goos: "wasip1", Goarch: "wasm", CoroProfile: CoroProfileStackless}, want: true},
		{name: "wasm-js", conf: &Config{Goos: "js", Goarch: "wasm", CoroProfile: CoroProfileStackless}, want: true},
		{name: "wasm-unknown", conf: &Config{Goos: "unknown", Goarch: "wasm", CoroProfile: CoroProfileStackless}, want: true},
		{name: "baremetal-config", conf: &Config{Goos: "linux", Goarch: "arm", Tags: "nogc,baremetal", CoroProfile: CoroProfileStackless}, want: true},
		{name: "baremetal-resolved-target", conf: &Config{Goos: "linux", Goarch: "arm", Target: "rp2040", resolvedTargetBuildTags: []string{"rp2040", "baremetal"}, CoroProfile: CoroProfileStackless}, want: true},
		{name: "explicit-embedded", conf: &Config{Goos: "linux", Goarch: "arm64", Tags: "llgo_coro_host", CoroProfile: CoroProfileStackless}, want: true},
		{name: "explicit-embedded-go-flags", conf: &Config{Goos: "linux", Goarch: "arm64", GoBuildFlags: []string{"-tags=llgo_coro_host"}, CoroProfile: CoroProfileStackless}, want: true},
		{name: "explicit-embedded-resolved-target", conf: &Config{Goos: "linux", Goarch: "arm64", Target: "board", resolvedTargetBuildTags: []string{"llgo_coro_host"}, CoroProfile: CoroProfileStackless}, want: true},
		{name: "native-pipe", conf: &Config{Goos: "linux", Goarch: "amd64", CoroProfile: CoroProfileStackless}},
		{name: "unsupported-windows", conf: &Config{Goos: "windows", Goarch: "amd64", CoroProfile: CoroProfileStackless}},
		{name: "test-adapter-wasm", conf: &Config{Goos: "wasip1", Goarch: "wasm", Tags: "coro_runtime_adapter_test", CoroProfile: CoroProfileStackless}},
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
	host := &Config{Goos: "wasip1", Goarch: "wasm", CoroProfile: CoroProfileStackless}
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
		&Config{Goos: "linux", Goarch: "amd64", CoroProfile: CoroProfileStackless}, true,
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
		{name: "disabled", conf: &Config{Goos: "linux", Goarch: "amd64"}},
		{name: "linux-amd64", conf: &Config{Goos: "linux", Goarch: "amd64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-arm64", conf: &Config{Goos: "linux", Goarch: "arm64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-loong64", conf: &Config{Goos: "linux", Goarch: "loong64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-ppc64", conf: &Config{Goos: "linux", Goarch: "ppc64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-ppc64le", conf: &Config{Goos: "linux", Goarch: "ppc64le", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-riscv64", conf: &Config{Goos: "linux", Goarch: "riscv64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "linux-s390x", conf: &Config{Goos: "linux", Goarch: "s390x", CoroProfile: CoroProfileStackless}, want: true},
		{name: "darwin-amd64", conf: &Config{Goos: "darwin", Goarch: "amd64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "darwin-arm64", conf: &Config{Goos: "darwin", Goarch: "arm64", CoroProfile: CoroProfileStackless}, want: true},
		{name: "darwin-loong64-not-a-supported-clock-target", conf: &Config{Goos: "darwin", Goarch: "loong64", CoroProfile: CoroProfileStackless}},
		{name: "linux-386-unverified-time-abi", conf: &Config{Goos: "linux", Goarch: "386", CoroProfile: CoroProfileStackless}},
		{name: "linux-arm-unverified-time-abi", conf: &Config{Goos: "linux", Goarch: "arm", CoroProfile: CoroProfileStackless}},
		{name: "windows-amd64", conf: &Config{Goos: "windows", Goarch: "amd64", CoroProfile: CoroProfileStackless}},
		{name: "named-target", conf: &Config{Goos: "linux", Goarch: "arm64", Target: "nintendoswitch", CoroProfile: CoroProfileStackless}},
		{name: "baremetal", conf: &Config{Goos: "linux", Goarch: "arm64", Tags: "baremetal", CoroProfile: CoroProfileStackless}},
		{name: "adapter-test", conf: &Config{Goos: "linux", Goarch: "amd64", Tags: "coro_runtime_adapter_test", CoroProfile: CoroProfileStackless}},
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
			conf:       &Config{Goos: "linux", Target: "nintendoswitch", CoroProfile: CoroProfileStackless},
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
		Goarch: "amd64", CoroProfile: CoroProfileStackless,
	}
	tags, err := effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tags, "llgo_coro_native_fleet") || !nativeCoroFleetRuntimeABI(conf) {
		t.Fatalf("configured native fleet tags/runtime = %q/%t", tags, nativeCoroFleetRuntimeABI(conf))
	}
	conf.CoroProfile = CoroProfileNone
	tags, err = effectiveBuildTags(conf, crosscompile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tags, "llgo_coro_native_fleet") || nativeCoroFleetRuntimeABI(conf) {
		t.Fatalf("disabled native fleet tags/runtime = %q/%t", tags, nativeCoroFleetRuntimeABI(conf))
	}
}

func TestEffectiveBuildTagsRejectsForgedNativeIngressTestCapability(t *testing.T) {
	conf := &Config{Tags: "nogc," + coroNativeIngressTestBuildTag}
	_, err := effectiveBuildTags(conf, crosscompile.Export{})
	if err == nil {
		t.Fatal("forged native ingress test capability was accepted")
	}
	for _, want := range []string{coroNativeIngressTestBuildTag, "Config.Tags", "compiler-reserved capability"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
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
				Goos: "linux", Goarch: "arm64", Target: "nintendoswitch", CoroProfile: CoroProfileStackless,
			},
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
	conf := &Config{Goos: "linux", Goarch: "amd64", CoroProfile: CoroProfileStackless}
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
			conf: &Config{Goos: "linux", CoroProfile: CoroProfileStackless},
			want: true,
		},
		{
			name: "named-linux-target",
			conf: &Config{Goos: "linux", Target: "nintendoswitch", CoroProfile: CoroProfileStackless},
		},
		{
			name: "runtime-adapter",
			conf: &Config{Goos: "linux", Tags: "coro_runtime_adapter_test", CoroProfile: CoroProfileStackless},
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
		}, CoroProfile: CoroProfileStackless,
	}
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
	conf.CoroProfile = CoroProfileStackless
	conf.CoroProfile = CoroProfileStackless
	conf.CoroProfile = CoroProfileStackless
	conf.CoroProfile = CoroProfileStackless
	conf.CoroProfile = CoroProfileStackless
	conf.CoroProfile = CoroProfileStackless
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			return nil, err
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
		const runtimePath = "github.com/goplus/llgo/runtime/internal/runtime"
		for _, want := range []struct {
			path     string
			name     string
			external bool
		}{
			{path: runtimePath, name: "coroTargetExecutorStartV1"},
			{path: runtimePath, name: "coroTargetBeginExecutorWaitV1"},
			{path: runtimePath, name: "coroTargetBeginExecutorCloseV1"},
			{path: runtimePath, name: coroNativePostWaitSymbolV1},
			{path: runtimePath, name: coroWaitPrepareSymbolV1},
			{path: runtimePath, name: coroWaitRollbackSymbolV1},
			{path: runtimePath, name: coroWaitRetireCompletedSymbolV1},
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
			if _, required := input.requiredPlain[function]; !required {
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
				if _, schedulerWait := plan.ForeignSchedulerWaitCertificate(function); !schedulerWait ||
					functionPlan.External != coro.ExternalUnknownForeign || functionPlan.Emission != coro.EmitExternal ||
					functionPlan.ManagedDemand != coro.NoDemand || !functionPlan.RawPlainDemand ||
					!functionPlan.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) {
					return nil, fmt.Errorf("native poll leaf plan = %+v, want exact raw schedulerwait external", functionPlan)
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
