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
	"bytes"
	stdcontext "context"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xgo-dev/llgo/internal/optlevel"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// Darwin clang normally wraps bitcode. LLGo's LLVM writer emits raw bitcode;
// the host ar accepts it but may omit its symbols from the archive index.
// Link and run a real native consumer to detect that silent omission.
func TestNativeRuntimeRawBitcodeArchiveRetainsSymbols(t *testing.T) {
	testRuntimeLibraryArchiveConsumer(t, true, "", "github.com/xgo-dev/llgo/runtime/internal/coro")
}

// A configured ordinary archiver keeps the ordinary native object pipeline.
// Linking that actual object with the configured tool checks the fallback.
func TestNativeRuntimeCustomArchiverKeepsNativeObjects(t *testing.T) {
	testRuntimeLibraryArchiveConsumer(t, false, "/usr/bin/ar", "github.com/xgo-dev/llgo/runtime/internal/coro")
}

func TestRuntimeLibraryOptimizationKeepsApplicationObjectsNative(t *testing.T) {
	testRuntimeLibraryArchiveConsumer(t, false, "", "example.com/application")
}

func testRuntimeLibraryArchiveConsumer(t *testing.T, rawProvider bool, archiver, pkgPath string) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native Darwin arm64 runtime library optimization")
	}
	t.Setenv("LLGO_AR", archiver)
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("archiveprobe", pkgPath)
	mod := pkg.Module()
	mod.SetDataLayout(prog.DataLayout())
	mod.SetTarget(prog.TargetSpec().Triple)
	lc := mod.Context()
	fn := llvm.AddFunction(mod, "llgo_runtime_archive_probe", llvm.FunctionType(lc.Int32Type(), nil, false))
	b := lc.NewBuilder()
	defer b.Dispose()
	b.SetInsertPointAtEnd(lc.AddBasicBlock(fn, "entry"))
	b.CreateRet(llvm.ConstInt(lc.Int32Type(), 7, false))
	conf := &Config{Mode: ModeBuild, BuildMode: BuildModeExe, Goos: "darwin", Goarch: "arm64", OptLevel: optlevel.Os}
	owner := &context{buildConf: conf, prog: prog}
	owner.crossCompile.LLVMTarget = prog.TargetSpec().Triple
	var buffer llvm.MemoryBuffer
	if rawProvider {
		buffer = llvm.WriteFullLTOBitcodeToMemoryBuffer(mod, false)
	} else {
		var err error
		buffer, _, err = emitObjectToMemoryBuffer(owner, pkg)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer buffer.Dispose()
	if !rawProvider {
		object, err := macho.NewFile(bytes.NewReader(buffer.Bytes()))
		if err != nil {
			t.Fatalf("application/custom-archiver provider is not a native Mach-O object: %v", err)
		}
		object.Close()
	}
	dir := t.TempDir()
	provider := filepath.Join(dir, "provider.o")
	if err := os.WriteFile(provider, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "runtime.a")
	if err := owner.createPackageArchiveFile(archive, &aPackage{Package: &packages.Package{PkgPath: pkg.Path()}, ObjFiles: []string{provider}}, false); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.c")
	if err := os.WriteFile(main, []byte("int llgo_runtime_archive_probe(void); int main(void) { return llgo_runtime_archive_probe() != 7; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	run := func(args ...string) {
		t.Helper()
		deadline, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(deadline, args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, out)
		}
	}
	run(clang, "-fuse-ld=lld", main, archive, "-o", binary)
	run(binary)
}

func TestRuntimeLibraryOptimizationFingerprint(t *testing.T) {
	baseline := newManifestBuilder()
	optimized := newManifestBuilder()
	optimized.common.RuntimeLibraryCodegen = runtimeLibraryOptimizationVersion
	if baseline.Fingerprint() == optimized.Fingerprint() {
		t.Fatal("different runtime library policies share a cache fingerprint")
	}
	if !strings.Contains(optimized.Build(), "RUNTIME_LIBRARY_OPTIMIZATION: "+runtimeLibraryOptimizationVersion) {
		t.Fatalf("manifest omitted runtime library policy:\n%s", optimized.Build())
	}
	next := newManifestBuilder()
	next.common.RuntimeLibraryCodegen = runtimeLibraryOptimizationVersion + "-next"
	if next.Fingerprint() == optimized.Fingerprint() {
		t.Fatal("changed runtime library version reused cache fingerprint")
	}
	optimized.common.RuntimeLibraryCodegen = ""
	if optimized.Fingerprint() != baseline.Fingerprint() {
		t.Fatal("disabled runtime library policy changed ordinary cache fingerprint")
	}
}
