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
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const runtimeNonblockingLeaseAtomic64Dir = "internal/coro"

func TestRuntimeNonblockingLeaseAtomic64TargetCertificationSelection(t *testing.T) {
	files := []string{
		"nonblocking_lease_atomic64_host.go",
		"nonblocking_lease_atomic64_llgo_native.go",
		"nonblocking_lease_atomic64_llgo_unsupported.go",
	}
	for _, test := range []struct {
		name   string
		goos   string
		goarch string
		tags   []string
		want   string
	}{
		{name: "host-darwin-arm64", goos: "darwin", goarch: "arm64", want: files[0]},
		{name: "native-darwin-arm64", goos: "darwin", goarch: "arm64", tags: []string{"llgo"}, want: files[1]},
		{name: "native-linux-amd64", goos: "linux", goarch: "amd64", tags: []string{"llgo"}, want: files[1]},
		{name: "linux-386", goos: "linux", goarch: "386", tags: []string{"llgo"}, want: files[2]},
		{name: "wasm", goos: "wasip1", goarch: "wasm", tags: []string{"llgo"}, want: files[2]},
		{name: "baremetal-arm64", goos: "linux", goarch: "arm64", tags: []string{"llgo", "baremetal"}, want: files[2]},
		{name: "unreviewed-native-os", goos: "windows", goarch: "amd64", tags: []string{"llgo"}, want: files[2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = test.goarch
			ctx.BuildTags = append([]string(nil), test.tags...)
			for _, file := range files {
				selected, err := ctx.MatchFile(runtimeNonblockingLeaseAtomic64Dir, file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if selected != (file == test.want) {
					t.Errorf("MatchFile(%q) = %t, want %t", file, selected, file == test.want)
				}
			}
		})
	}
}

func TestRuntimeNonblockingLeaseAtomic64CertificationIsSemanticAndFailClosed(t *testing.T) {
	read := func(name string) string {
		source, err := os.ReadFile(filepath.Join(runtimeNonblockingLeaseAtomic64Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(source)
	}
	if source := read("nonblocking_lease_atomic64_host.go"); !strings.Contains(source, "const nonblockingLeaseAtomic64Bounded = true") {
		t.Fatal("host atomic64 profile is not enabled for state-machine tests")
	}
	if source := read("nonblocking_lease_atomic64_llgo_native.go"); !strings.Contains(source, "const nonblockingLeaseAtomic64Bounded = true") ||
		!strings.Contains(source, "semantic certification") {
		t.Fatal("native atomic64 profile lacks explicit bounded semantic certification")
	}
	if source := read("nonblocking_lease_atomic64_llgo_unsupported.go"); !strings.Contains(source, "const nonblockingLeaseAtomic64Bounded = false") ||
		!strings.Contains(source, "__atomic_*_8 lock fallback") ||
		strings.Contains(source, "clite/sync/atomic") ||
		!strings.Contains(source, "func preemptCompareAndSwap64(_ *uint64, _, _ uint64) bool") {
		t.Fatal("unreviewed atomic64 targets do not fail closed")
	}
	leaseSource, err := os.ReadFile(filepath.Join(runtimeNonblockingLeaseAtomic64Dir, "nonblocking_lease.go"))
	if err != nil {
		t.Fatal(err)
	}
	usable := string(leaseSource)
	start := strings.Index(usable, "func nonblockingLeaseGateUsable(")
	if start < 0 {
		t.Fatal("lease core has no atomic64 capability gate")
	}
	usable = usable[start:]
	capability := strings.Index(usable, "return nonblockingLeaseAtomic64Bounded &&")
	alignment := strings.Index(usable, "nonblockingLeaseGateAligned(gate)")
	if capability < 0 || alignment < 0 || capability > alignment {
		t.Fatal("lease gate does not test target atomic64 capability before address suitability")
	}
}
