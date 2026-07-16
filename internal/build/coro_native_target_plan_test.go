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
	"testing"

	"github.com/goplus/llgo/internal/coro"
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
		{name: "linux", conf: &Config{Goos: "linux", EnableCoroProgramBootstrapRun: true}, want: true},
		{name: "darwin", conf: &Config{Goos: "darwin", EnableCoroProgramBootstrapRun: true}, want: true},
		{name: "windows", conf: &Config{Goos: "windows", EnableCoroProgramBootstrapRun: true}},
		{name: "named-target", conf: &Config{Goos: "linux", Target: "rp2040", EnableCoroProgramBootstrapRun: true}},
		{name: "baremetal-comma", conf: &Config{Goos: "linux", Tags: "nogc,baremetal,cortexm", EnableCoroProgramBootstrapRun: true}},
		{name: "baremetal-space", conf: &Config{Goos: "linux", Tags: "nogc baremetal cortexm", EnableCoroProgramBootstrapRun: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeCoroDoorbellRuntimeABI(test.conf); got != test.want {
				t.Fatalf("native coroutine doorbell selection = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRealNativeCoroTargetIsTrustedPlainSchedulerIsland(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine target plan requires Darwin or Linux")
	}
	sentinel := errors.New("native target plan verified")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.EnableCoroEntryResolution = true
	conf.EnableCoroPhysicalABI = true
	conf.EnableCoroChildAwait = true
	conf.EnableCoroPlainDispatch = true
	conf.EnableCoroProgramBootstrapABI = true
	conf.EnableCoroProgramBootstrapRun = true
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
			if !ok || functionPlan.Effect != coro.NoSuspend || functionPlan.Exec.Contains(coro.NeedsPreempt|coro.BlockForeign) {
				return nil, fmt.Errorf("native target function %s.%s plan = %+v, present=%t", want.path, want.name, functionPlan, ok)
			}
			if want.external {
				if functionPlan.External != coro.ExternalKnown || functionPlan.Emission != coro.EmitExternal {
					return nil, fmt.Errorf("native poll leaf plan = %+v, want exact known external", functionPlan)
				}
			} else if functionPlan.External != coro.Defined || functionPlan.Emission != coro.EmitPlain ||
				functionPlan.Primary != coro.PrimaryPlain || functionPlan.FuncRep != coro.DirectPlain {
				return nil, fmt.Errorf("native target body %s.%s plan = %+v, want direct plain", want.path, want.name, functionPlan)
			}
		}
		return nil, sentinel
	}
	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want verified native target plan", err)
	}
}
