//go:build !llgo

package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/cl"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const simd128Source = `package main
import "simd/archsimd"
import "runtime"

//go:noinline
func add(x, y archsimd.Float32x4) archsimd.Float32x4 { return x.Add(y).Sub(y) }
//go:noinline
func bits(x, y archsimd.Uint64x2, i uint8) uint64 {
 z := x.Add(y).Sub(y).And(y).Or(x).Xor(y)
 return z.SetElem(i, x.GetElem(i)).GetElem(i)
}
type storedVector archsimd.Float32x4
func convert(x storedVector) archsimd.Float32x4 { return archsimd.Float32x4(x) }
//go:noinline
func identity(x archsimd.Float32x4) archsimd.Float32x4 { return x }
//go:noinline
func loop(x archsimd.Float32x4, n int) (archsimd.Float32x4, int) {
 for i := 0; i < n; i++ { x = x.Add(x) }
 return x, n
}
func boxed(x any) archsimd.Float32x4 { return x.(archsimd.Float32x4) }
func invoke(x, y archsimd.Float32x4) { defer x.Add(y); go x.Sub(y) }
func main() {
 _ = runtime.FuncForPC(0)
 var x archsimd.Float32x4
 _ = add(x, x)
 x, _ = loop(identity(convert(storedVector(x))), 2)
 invoke(x, x)
 var y archsimd.Uint64x2
 _ = bits(y, y, 0)
}
`

func simdTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"go.mod": "module simdtest\n\ngo 1.27\n", "main.go": simd128Source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSIMD128LLVM(t *testing.T) {
	dir := simdTestDir(t)
	for _, target := range []struct{ os, arch string }{{"linux", "amd64"}, {"linux", "arm64"}, {"wasip1", "wasm"}} {
		t.Run(target.arch, func(t *testing.T) {
			conf := NewDefaultConf(ModeGen)
			conf.Goos, conf.Goarch, conf.GOEXPERIMENT = target.os, target.arch, "simd"
			pkgs, err := Build(Invocation{Args: []string{"."}, Config: conf, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if len(pkgs) != 1 {
				t.Fatalf("packages: %d", len(pkgs))
			}
			defer pkgs[0].LPkg.Prog.Dispose()
			mod := pkgs[0].LPkg.Module()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			fn := mod.NamedFunction("main.add")
			if fn.IsNil() || !strings.Contains(fn.String(), "fadd <4 x float>") {
				t.Fatal("Float32x4.Add did not lower to vector fadd")
			}
			if strings.Contains(fn.String(), "extractvalue") || strings.Contains(fn.String(), "insertelement") || strings.Contains(fn.String(), "extractelement") {
				t.Fatalf("arithmetic repacks vectors:\n%s", fn.String())
			}
			identity := mod.NamedFunction("main.identity")
			if identity.GlobalValueType().ReturnType().TypeKind() != llvm.VectorTypeKind || identity.GlobalValueType().ParamTypes()[0].TypeKind() != llvm.VectorTypeKind {
				t.Fatalf("identity does not use a vector ABI:\n%s", identity.String())
			}
			if !strings.Contains(mod.NamedFunction("main.loop").String(), "phi <4 x float>") {
				t.Fatal("missing vector phi")
			}
			if target.arch == "wasm" {
				// Attributes are printed separately by LLVM; inspect the function itself.
				found := false
				for _, attr := range identity.GetFunctionAttributes() {
					if attr.IsString() && attr.GetStringKind() == "target-features" && strings.Contains(attr.GetStringValue(), "+simd128") {
						found = true
					}
				}
				if !found {
					t.Fatal("identity lacks wasm SIMD feature")
				}
			}
			prog := pkgs[0].LPkg.Prog
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.Target().Spec().Triple)
			opts := llvm.NewPassBuilderOptions()
			defer opts.Dispose()
			opts.SetVerifyEach(true)
			if err := mod.RunPasses("default<O2>", prog.TargetMachine(), opts); err != nil {
				t.Fatal(err)
			}
			asm, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.AssemblyFile)
			if err != nil {
				t.Fatal(err)
			}
			defer asm.Dispose()
			want := map[string]string{"amd64": "addps", "arm64": "fadd", "wasm": "f32x4.add"}[target.arch]
			if !strings.Contains(string(asm.Bytes()), want) {
				t.Fatalf("missing %s in assembly", want)
			}

		})
	}
}

// COFF checks even relocations in dead functions. Keep unsupported operations
// unresolved when reachable, but do not emit dead references to them.
func TestSIMDWindowsDeadBodies(t *testing.T) {
	const src = `package archsimd
 func Missing()
 func Dead() { Missing() }
 func Live() {}
 //go:linkname Alias simd/archsimd.Dead
 func Alias()
 `
	for _, live := range []string{"Live", "Dead", "Alias"} {
		t.Run(live, func(t *testing.T) {
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, "simd.go", src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			pkg, _, err := ssautil.BuildPackage(&types.Config{}, fs, types.NewPackage("simd/archsimd", "archsimd"), []*ast.File{file}, ssa.SanityCheckFunctions)
			if err != nil {
				t.Fatal(err)
			}
			prog := llssa.NewProgram(&llssa.Target{GOOS: "windows", GOARCH: "amd64"})
			defer prog.Dispose()
			if err := cl.ParsePkgSyntax(prog, fs, pkg.Pkg, []*ast.File{file}); err != nil {
				t.Fatal(err)
			}
			use := analyzeWasmProgramUseWithLinknames(pkg.Prog, []*ssa.Function{pkg.Func(live)}, prog)
			if !analyzeWasmProgramUse(pkg.Prog, nil).keepsSIMDBody(pkg.Func("Dead")) {
				t.Fatal("unrooted analysis discarded a body")
			}
			out, _, err := cl.NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, pkg, []*ast.File{file}, nil, false, cl.Options{FuncBodyFilter: use.keepsSIMDBody})
			if err != nil {
				t.Fatal(err)
			}
			mod := out.Module()
			ctx := &context{prog: prog, progSSA: pkg.Prog, buildConf: &Config{Goos: "windows", BuildMode: BuildModeExe}}
			if !windowsSIMDReachability(ctx) {
				t.Fatal("Windows SIMD analysis was not enabled")
			}
			ctx.wasmProgramUse = use
			manifest := newManifestBuilder()
			ctx.collectCommonInputs(manifest)
			if manifest.common.ReachabilityScope != use.funcInfoKey {
				t.Fatal("Windows SIMD cache omitted program reachability")
			}
			ctx.buildConf.BuildMode = BuildModeCShared
			if windowsSIMDReachability(ctx) {
				t.Fatal("library was restricted to executable roots")
			}

			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			dead := mod.NamedFunction("simd/archsimd.Dead").String()
			missing := mod.NamedFunction("simd/archsimd.Missing")
			if live == "Live" {
				if strings.Contains(dead, "call") || !strings.Contains(dead, "unreachable") || missing.IsDeclaration() {
					t.Fatalf("dead references survived:\n%s", mod.String())
				}
			} else if !strings.Contains(dead, "call") || !missing.IsDeclaration() {
				t.Fatalf("reachable unsupported operation was hidden:\n%s", mod.String())
			}
			linker, err := exec.LookPath("lld-link")
			if err != nil {
				t.Skip("lld-link unavailable for COFF link check")
			}
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.Target().Spec().Triple)
			buf, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Dispose()
			dir := t.TempDir()
			obj := filepath.Join(dir, "simd.obj")
			if err := os.WriteFile(obj, buf.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(linker, "/dll", "/noentry", "/nodefaultlib", "/opt:ref", "/include:simd/archsimd."+live, "/out:"+filepath.Join(dir, "simd.dll"), obj).CombinedOutput()
			if live == "Live" && err != nil {
				t.Fatalf("dead SIMD references prevented COFF linking: %v\n%s", err, output)
			}
			if live != "Live" && (err == nil || !strings.Contains(string(output), "undefined symbol: simd/archsimd.Missing")) {
				t.Fatalf("reachable unsupported SIMD did not fail linking: %v\n%s", err, output)
			}

		})
	}
}

func TestSIMDWindowsPackage(t *testing.T) {
	dir := simdTestDir(t)
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			conf := NewDefaultConf(ModeGen)
			conf.Goos, conf.Goarch, conf.GOEXPERIMENT = "windows", arch, "simd"
			pkgs, err := Build(Invocation{Args: []string{".", "simd/archsimd"}, Config: conf, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, pkg := range pkgs {
				defer pkg.LPkg.Prog.Dispose()
				if pkg.PkgPath != "simd/archsimd" {
					continue
				}
				found = true
				mod := pkg.LPkg.Module()
				if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
				for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
					if strings.HasPrefix(fn.Name(), "simd/archsimd.") && fn.IsDeclaration() && !fn.FirstUse().IsNil() {
						t.Errorf("unused unsupported operation still referenced: %s", fn.Name())
					}
				}
			}
			if !found {
				t.Fatal("archsimd package was not compiled")
			}
		})
	}
}

func TestSIMDWindowsLinkname(t *testing.T) {
	dir := simdTestDir(t)
	const source = `package main
import "simd/archsimd"
import _ "unsafe"
//go:linkname broadcast simd/archsimd.BroadcastFloat32x4
func broadcast(float32) archsimd.Float32x4
func main() { println(broadcast(1).GetElem(0)) }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	conf := NewDefaultConf(ModeGen)
	conf.Goos, conf.Goarch, conf.GOEXPERIMENT = "windows", "amd64", "simd"
	pkgs, err := Build(Invocation{Args: []string{".", "simd/archsimd"}, Config: conf, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer pkgs[0].LPkg.Prog.Dispose()
	for _, pkg := range pkgs {
		if pkg.PkgPath != "simd/archsimd" {
			continue
		}
		mod := pkg.LPkg.Module()
		fn := mod.NamedFunction("simd/archsimd.BroadcastFloat32x4")
		if fn.IsNil() || !strings.Contains(fn.String(), "broadcast1To4") {
			t.Fatal("linkname target body was discarded")
		}
		callee := mod.NamedFunction("simd/archsimd.Float32x4.broadcast1To4")
		if callee.IsNil() || !callee.IsDeclaration() {
			t.Fatal("transitive unsupported intrinsic was hidden")
		}
		return
	}
	t.Fatal("missing archsimd package")
}
