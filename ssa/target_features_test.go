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

package ssa

import (
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestTargetArchitectureFeatures(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		wantCPU  string
		wantFeat string
	}{
		{name: "386 default", target: Target{GOOS: "windows", GOARCH: "386"}, wantCPU: "pentium4", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "386 softfloat", target: Target{GOOS: "windows", GOARCH: "386", GO386: "softfloat"}, wantCPU: "pentium4", wantFeat: "+cx8,+fxsr,+mmx,+soft-float,-sse,-sse2,-x87"},
		{name: "amd64 v1", target: Target{GOOS: "windows", GOARCH: "amd64"}, wantCPU: "x86-64", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "amd64 v2", target: Target{GOOS: "windows", GOARCH: "amd64", GOAMD64: "v2"}, wantCPU: "x86-64-v2", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "amd64 v3", target: Target{GOOS: "windows", GOARCH: "amd64", GOAMD64: "v3"}, wantCPU: "x86-64-v3", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "amd64 v4", target: Target{GOOS: "windows", GOARCH: "amd64", GOAMD64: "v4"}, wantCPU: "x86-64-v4", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "arm64 default", target: Target{GOOS: "windows", GOARCH: "arm64"}, wantCPU: "generic", wantFeat: "+neon,-fmv"},
		{name: "arm64 v8.1", target: Target{GOOS: "windows", GOARCH: "arm64", GOARM64: "v8.1"}, wantCPU: "generic", wantFeat: "+v8.1a,+neon,+lse,-fmv"},
		{name: "arm64 v9 crypto", target: Target{GOOS: "windows", GOARCH: "arm64", GOARM64: "v9.0,crypto"}, wantCPU: "generic", wantFeat: "+v9a,+neon,+lse,+crypto,-fmv"},
		{name: "darwin arm64", target: Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.2"}, wantCPU: "generic", wantFeat: "+v8.2a,+neon,+lse"},
		{name: "named 386 target unchanged", target: Target{GOOS: "linux", GOARCH: "386", GO386: "softfloat", Target: "custom"}, wantCPU: "pentium4", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "named amd64 target unchanged", target: Target{GOOS: "linux", GOARCH: "amd64", GOAMD64: "v4", Target: "custom"}, wantCPU: "x86-64", wantFeat: "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"},
		{name: "named arm64 target unchanged", target: Target{GOOS: "linux", GOARCH: "arm64", GOARM64: "v9.5,crypto", Target: "custom"}, wantCPU: "generic", wantFeat: "+neon,-fmv"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := test.target.Spec()
			if spec.CPU != test.wantCPU || spec.Features != test.wantFeat {
				t.Fatalf("target spec = CPU %q, features %q; want CPU %q, features %q", spec.CPU, spec.Features, test.wantCPU, test.wantFeat)
			}
		})
	}
}

func TestArchitectureFeatureTargetMachines(t *testing.T) {
	for _, target := range []*Target{
		{GOOS: "windows", GOARCH: "386", GO386: "softfloat"},
		{GOOS: "windows", GOARCH: "amd64", GOAMD64: "v4"},
		{GOOS: "windows", GOARCH: "arm64", GOARM64: "v9.5,crypto"},
	} {
		prog := NewProgram(target)
		pkg := prog.NewPackage("p", "example.com/p")
		pkg.NewFunc("example.com/p.f", NoArgsNoRet, InGo).MakeBody(1).Return()
		buf, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
		if err != nil {
			prog.Dispose()
			t.Fatalf("emit %s/%s with CPU/features %q/%q: %v", target.GOOS, target.GOARCH, target.Spec().CPU, target.Spec().Features, err)
		}
		buf.Dispose()
		prog.Dispose()
	}
}
