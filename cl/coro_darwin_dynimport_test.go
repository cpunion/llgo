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
	"go/ast"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroDarwinImportFixture = `package unix

//llgo:link syscall6 llgo.syscall32
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, uintptr)

var libc_openat_trampoline_addr uintptr

//go:cgo_import_dynamic libc_openat openat "/usr/lib/libSystem.B.dylib"

func Open(a uintptr) uintptr {
	r1, _, _ := syscall6(libc_openat_trampoline_addr, a, 0, 0, 0, 0, 0)
	return r1
}
`

func TestCoroDarwinDynimportCertifiesExactReadOnlyAddress(t *testing.T) {
	const sdk = `package syscall
func libc_openat_trampoline()
`
	const sdkUse = `
//llgo:link sdkPC llgo.funcPCABI0
func sdkPC(fn any) uintptr

//llgo:link sdkSyscall6 llgo.syscall32
func sdkSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, uintptr)

func SDKOpen(a uintptr) uintptr {
	r1, _, _ := sdkSyscall6(sdkPC(libc_openat_trampoline), a, 0, 0, 0, 0, 0)
	return r1
}
`
	for _, test := range []struct {
		name, source string
		patched      bool
		certified    bool
		sdkUse       string
	}{
		{"catalog and import", coroDarwinImportFixture, true, true, ""},
		{"exact SDK transport", coroDarwinImportFixture, true, true, sdkUse},
		{"different SDK transport", strings.NewReplacer(
			"syscall6(fn, a1, a2, a3, a4, a5, a6", "syscall6(fn, a1, a2, a3",
			"addr, a, 0, 0, 0, 0, 0)", "addr, a, 0, 0)",
		).Replace(coroDarwinImportFixture), true, false, sdkUse},
		{"untrusted catalog", coroDarwinImportFixture, false, false, ""},
		{"foreign library", strings.Replace(coroDarwinImportFixture,
			"/usr/lib/libSystem.B.dylib", "/tmp/untrusted.dylib", 1), true, false, ""},
		{"mismatched import", strings.Replace(coroDarwinImportFixture,
			"libc_openat openat", "libc_openat open", 1), true, false, ""},
		{"mutated word", coroDarwinImportFixture + "\nfunc Rebind(a uintptr) { libc_openat_trampoline_addr = a }",
			true, false, ""},
		{"escaped word", coroDarwinImportFixture + "\nfunc Slot() *uintptr { return &libc_openat_trampoline_addr }",
			true, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			syscall := testProg.addPackage(t, "syscall", sdk+test.sdkUse)
			unix := testProg.addPackage(t, "golang.org/x/sys/unix", test.source)
			testProg.ssa.Build()
			prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "darwin", GOARCH: "arm64"})
			defer prog.Dispose()
			prog.SetLinkname("syscall.libc_openat_trampoline", "C.llgo_openat")
			files := []EmissionPackage{
				{SSA: syscall.ssa, Files: []*ast.File{syscall.file}},
				{SSA: unix.ssa, Files: []*ast.File{unix.file}},
			}
			if test.patched {
				files[0].CompilerSourcePatchFiles = []string{testProg.fset.Position(syscall.file.Package).Filename}
			}
			universe, err := prepareStacklessEmissionUniverseWithOptions(
				prog, nil, files, EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
			)
			if err != nil {
				t.Fatal(err)
			}
			call := exactWorkerSyscallCall(t, universe, unix.ssa.Func("Open"))
			certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
			if err != nil || certified != test.certified {
				t.Fatalf("worker certificate = %+v, %t, %v; want certified=%t", certificate, certified, err, test.certified)
			}
			if test.sdkUse != "" {
				sdkCall := exactWorkerSyscallCall(t, universe, syscall.ssa.Func("SDKOpen"))
				if _, ok, err := universe.CoroWorkerSyscallCertificate(sdkCall); !ok || err != nil {
					t.Fatalf("SDK certificate = %t, %v", ok, err)
				}
			}
			if !certified {
				return
			}
			analysis, err := AnalyzeCoroCallableShadows(universe)
			if err != nil {
				t.Fatal(err)
			}
			sink, ok := analysis.Sink(call)
			if !ok || len(sink.Candidates) != 1 || certificate.StaticTargetCount != 1 {
				t.Fatalf("worker shadow = %+v, %t; certificate = %+v", sink, ok, certificate)
			}
			shadow := sink.Candidates[0]
			_, load := shadow.Producer.(*ssa.UnOp)
			if !load || shadow.PhysicalSymbol != "llgo_openat" || shadow.ABI.WordArgs != 6 {
				t.Fatalf("worker shadow = %+v", shadow)
			}
		})
	}
}
