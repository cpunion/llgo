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
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
)

func TestCoroProgramManifestExtractsRootArchiveMember(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("final-link root extraction test requires Darwin or Linux")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ar, err := exec.LookPath("llvm-ar")
	if err != nil {
		ar, err = exec.LookPath("ar")
		if err != nil {
			t.Skip("llvm-ar/ar is unavailable")
		}
	}
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Skip("nm is unavailable")
	}

	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	temp := t.TempDir()
	emit := func(name string, pkg llssa.Package) string {
		t.Helper()
		pkg.Module().SetDataLayout(prog.DataLayout())
		pkg.Module().SetTarget(prog.TargetSpec().Triple)
		if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
			t.Fatalf("verify %s: %v\n%s", name, err, pkg.String())
		}
		object, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
		if err != nil {
			t.Fatalf("emit %s: %v\n%s", name, err, pkg.String())
		}
		defer object.Dispose()
		path := filepath.Join(temp, name+".o")
		if err := os.WriteFile(path, object.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	const (
		factoryName    = "__llgo_test_root_factory_v1"
		descriptorName = "__llgo_test_root_descriptor_v1"
		anchorName     = "__llgo_coro_root_package_v1.0123456789abcdef0123456789abcdef"
	)
	rootPkg := prog.NewPackage("root", "example.com/root")
	pointer := types.Typ[types.UnsafePointer]
	factory := rootPkg.NewFunc(factoryName, newSignature(
		[]types.Type{pointer, pointer, pointer},
		[]types.Type{pointer},
	), llssa.InC)
	factoryBody := factory.MakeBody(1)
	factoryBody.Return(prog.Nil(prog.VoidPtr()))
	descriptor := rootPkg.NewCoroRootFactoryDescriptor(descriptorName, llssa.CoroRootFactoryDescriptorOptions{
		Version: 1,
		Factory: factory.Expr,
		Startup: prog.Byte(),
		Result:  prog.Byte(),
	})
	rootPkg.NewCoroRootPackageAnchor(anchorName, llssa.CoroRootPackageAnchorOptions{
		Version:     1,
		Descriptors: []llssa.Expr{descriptor},
	})
	rootPkg.MaterializePreserveSyms()
	rootObject := emit("root", rootPkg)
	archive := filepath.Join(temp, "libroot.a")
	if output, err := exec.Command(ar, "rcs", archive, rootObject).CombinedOutput(); err != nil {
		t.Fatalf("archive root object: %v\n%s", err, output)
	}

	entryPkg := prog.NewPackage("entry", "entry")
	anchorType := prog.Struct(
		prog.Uint32(), prog.Uint32(), prog.Uint64(), prog.Uint64(), prog.Uintptr(), prog.VoidPtr(),
	)
	anchor := entryPkg.NewVarEx(anchorName, prog.Pointer(anchorType))
	entryPkg.Module().NamedGlobal(anchorName).SetLinkage(llvm.ExternalLinkage)
	entryPkg.Module().NamedGlobal(anchorName).SetVisibility(llvm.HiddenVisibility)
	entryPkg.NewCoroProgramManifest(coroProgramManifestSymbolV1, llssa.CoroProgramManifestOptions{
		Version:        1,
		PackageAnchors: []llssa.Expr{anchor.Expr},
	})
	main := entryPkg.NewFunc("main", newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InC)
	mainBody := main.MakeBody(1)
	mainBody.Return(prog.IntVal(0, prog.Int32()))
	entryPkg.MaterializePreserveSyms()
	entryObject := emit("entry", entryPkg)

	executable := filepath.Join(temp, "root-extract")
	args := []string{entryObject, archive, "-o", executable}
	if runtime.GOOS == "darwin" {
		args = append(args, "-Wl,-dead_strip")
	} else {
		args = append(args, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, args...).CombinedOutput(); err != nil {
		t.Fatalf("link root archive without whole-archive: %v\n%s", err, output)
	}
	output, err := exec.Command(nm, executable).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect linked root symbols: %v\n%s", err, output)
	}
	symbols := string(output)
	for _, want := range []string{anchorName, descriptorName, factoryName, coroProgramManifestSymbolV1} {
		if !strings.Contains(symbols, want) {
			t.Fatalf("final link lost %q after archive extraction/dead strip:\n%s", want, symbols)
		}
	}
}
