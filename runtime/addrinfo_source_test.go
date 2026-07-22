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

func TestDebugBackendSelectsLogicalWebAssemblyTargets(t *testing.T) {
	dir := filepath.Join("internal", "clite", "debug")
	tests := []struct {
		name      string
		goos      string
		goarch    string
		buildTags []string
		want      string
	}{
		{name: "native arm", goos: "linux", goarch: "arm", want: "debug.go"},
		{name: "standard wasm", goos: "wasip1", goarch: "wasm", want: "debug_webassembly.go"},
		{name: "arm frontend wasm backend", goos: "linux", goarch: "arm", buildTags: []string{"tinygo.wasm", "wasip2"}, want: "debug_webassembly.go"},
		{name: "bare metal", goos: "linux", goarch: "arm", buildTags: []string{"baremetal"}, want: "debug_baremetal.go"},
	}
	files := []string{"debug.go", "debug_webassembly.go", "debug_baremetal.go"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = test.goarch
			ctx.BuildTags = append([]string(nil), test.buildTags...)
			for _, file := range files {
				matched, err := ctx.MatchFile(dir, file)
				if err != nil {
					t.Fatal(err)
				}
				if matched != (file == test.want) {
					t.Errorf("%s selected=%v, want selected backend %s", file, matched, test.want)
				}
			}
		})
	}
}

func TestAddrinfoKeepsProgramCountersScalarAcrossTheCBoundary(t *testing.T) {
	for _, path := range []string{
		"internal/clite/debug/debug.go",
		"internal/clite/debug/debug_webassembly.go",
		"internal/clite/debug/debug_baremetal.go",
	} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		for _, required := range []string{
			"Fbase uintptr",
			"Saddr uintptr",
			"func Addrinfo(addr uintptr, info *Info) c.Int",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s lacks scalar address ABI %q", path, required)
			}
		}
		if strings.Contains(text, "func Addrinfo(addr unsafe.Pointer") {
			t.Errorf("%s exposes a program counter as a GC pointer", path)
		}
	}
	nativeSource, err := os.ReadFile("internal/clite/debug/debug.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nativeSource), "//llgo:coro noblock\n//go:linkname Addrinfo C.llgo_addrinfo") {
		t.Error("native Addrinfo is not classified as an exact bounded foreign leaf")
	}
	for _, required := range []string{
		"type stacktraceFrame struct",
		"func stacktrace(skip c.Int, frames *stacktraceFrame, capacity c.Int) c.Int",
		"Frame{uintptr(raw.pc), raw.offset, raw.sp",
		"func printStack(skip c.Int)",
		"printStack(c.Int(skip + 2))",
	} {
		if !strings.Contains(string(nativeSource), required) {
			t.Errorf("native stacktrace lacks exact code/scalar address split %q", required)
		}
	}

	cSource, err := os.ReadFile("internal/clite/debug/_wrap/debug.c")
	if err != nil {
		t.Fatal(err)
	}
	cText := string(cSource)
	for _, required := range []string{
		"int llgo_addrinfo(uintptr_t addr, Dl_info *info)",
		"dladdr((void *)addr, info)",
		"int llgo_stacktrace(int skip, llgo_stacktrace_frame *frames, int capacity)",
		"fn(ctx, (void *)pc, offset, (void *)fp",
		"void llgo_print_stack(int skip)",
		"llgo_walk_stack(skip + 1, NULL, llgo_print_stack_frame)",
	} {
		if !strings.Contains(cText, required) {
			t.Errorf("debug.c lacks exact scalar-to-native address boundary %q", required)
		}
	}

	for _, path := range []string{
		"internal/lib/runtime/symtab.go",
		"internal/runtime/caller.go",
	} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if strings.Contains(text, "Addrinfo(unsafe.Pointer(") {
			t.Errorf("%s reconstructs a GC pointer from a program counter", path)
		}
	}
}
