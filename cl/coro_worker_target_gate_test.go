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
	"runtime"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroWorkerNativeProgramTargetGate(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	tests := []struct {
		name   string
		target *llssa.Target
		want   string
	}{
		{name: "host"},
		{name: "linux", target: &llssa.Target{GOOS: "linux", GOARCH: "amd64"}},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, want: `GOARCH "wasm"`},
		{name: "named", target: &llssa.Target{GOOS: "linux", GOARCH: "amd64", Target: "rp2040"}, want: `named target "rp2040"`},
		{name: "non native os", target: &llssa.Target{GOOS: "windows", GOARCH: "amd64"}, want: `GOOS "windows"`},
		{
			name: "hidden wasm triple",
			target: &llssa.Target{
				GOOS: "linux", GOARCH: "amd64",
				Resolved: &llssa.TargetSpec{Triple: "wasm32-unknown-unknown"},
			},
			want: "requested LLVM triple",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := newLLSSAProgForTarget(t, test.target)
			defer prog.Dispose()
			err := validateCoroWorkerNativeProgramTarget(prog)
			if test.want == "" {
				if err != nil {
					t.Fatalf("native target gate rejected %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) ||
				!strings.Contains(err.Error(), coroWorkerNativeTargetRequirement) {
				t.Fatalf("target gate error = %v; want %q and adapter requirement", err, test.want)
			}
		})
	}
}

func TestCoroWorkerCompilationPreflightUsesEmissionUniverseTarget(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	compilation := &Compilation{
		EmissionUniverse: &EmissionUniverse{
			prog:             prog,
			coroCapabilities: CoroNativeTargetCapabilities(),
		},
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	err := compilation.preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), `GOARCH "wasm"`) {
		t.Fatalf("worker preflight target error = %v", err)
	}
}

func TestCoroWorkerCompilationBindsExactCodegenProgram(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	native := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "linux", GOARCH: "amd64"})
	defer native.Dispose()
	otherNative := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "linux", GOARCH: "amd64"})
	defer otherNative.Dispose()
	compilation := &Compilation{
		EmissionUniverse: &EmissionUniverse{
			prog:             native,
			coroCapabilities: CoroNativeTargetCapabilities(),
		},
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	if err := compilation.validateCoroWorkerCodegenProgram(native); err != nil {
		t.Fatalf("exact native program rejected: %v", err)
	}
	if err := compilation.validateCoroWorkerCodegenProgram(otherNative); err == nil ||
		!strings.Contains(err.Error(), "exact LLVM program") {
		t.Fatalf("mismatched codegen program error = %v", err)
	}
}
