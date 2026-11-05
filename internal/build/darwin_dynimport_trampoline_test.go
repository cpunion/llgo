package build

import (
	"debug/macho"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xgo-dev/llgo/internal/cabi"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	gopackages "golang.org/x/tools/go/packages"
)

const dynimportTrampolineDeclarations = `package nativebridge
//go:cgo_import_dynamic imported_word fixture_native_word "/usr/lib/libSystem.B.dylib"
func imported_word_trampoline()
//go:cgo_import_dynamic imported_float fixture_native_float "/usr/lib/libSystem.B.dylib"
func imported_float_trampoline()
//go:cgo_import_dynamic imported_pair fixture_native_pair "/usr/lib/libSystem.B.dylib"
func imported_pair_trampoline()
`

const dynimportTrampolineAssembly = `#include "textflag.h"
TEXT ·imported_word_trampoline(SB),NOSPLIT,$0-0
 JMP imported_word(SB)
TEXT ·imported_float_trampoline(SB),NOSPLIT,$0-0
 JMP imported_float(SB)
TEXT ·imported_pair_trampoline(SB),NOSPLIT,$0-0
 JMP imported_pair(SB)
`

func dynimportTrampolineFiles() map[string]string {
	return map[string]string{
		"go.mod":  "module example.com/nativebridge\n\ngo 1.20\n",
		"main.go": dynimportTrampolineDeclarations,
		"entry.s": dynimportTrampolineAssembly,
	}
}

func TestDarwinDynimportTrampolineOriginalGo(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			directory := nativeAddressFixtureWrite(t, dynimportTrampolineFiles())
			command := abi0FixtureCommand(t, 2*time.Minute,
				filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p=1", "-mod=readonly",
				"-buildmode=archive", "-o", filepath.Join(directory, "original.a"), ".")
			command.Dir = directory
			command.Env = withEnv(os.Environ(), "GOROOT="+runtime.GOROOT(),
				"GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "GOFLAGS=",
				"GOPROXY=off", "CGO_ENABLED=0", "GOOS=darwin", "GOARCH="+arch)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("complete original Go package and assembly: %v\n%s", err, output)
			}
		})
	}
}

func TestDarwinDynimportTrampolineNativeObjects(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			triple := arch + "-apple-darwin"
			if arch == "amd64" {
				triple = "x86_64-apple-darwin"
			}
			ctx, pkg := nativeAddressFixturePackageAtPath(t, dynimportTrampolineFiles(),
				"example.com/nativebridge", "darwin", arch, triple)
			ctx.cTransformer = cabi.NewTransformer(ctx.prog, triple, "", true)
			// The ordinary Go signature is deliberately void(). It cannot grant
			// the foreign function's physical arguments or result transport.
			objects, err := compileSelectedPkgSFiles(ctx, pkg, pkg.Package,
				[]string{filepath.Join(pkg.Dir, "entry.s")}, false)
			for _, object := range objects {
				t.Cleanup(func() { os.Remove(object) })
			}
			if err != nil {
				t.Fatalf("source-owned C trampoline must use its native producer: %v", err)
			}
			if len(objects) != 0 {
				t.Fatalf("duplicate declaration-driven objects: %v", objects)
			}
			aliases, err := buildGoCgoAliasObjects(ctx, pkg.PkgPath, pkg.Syntax, false)
			for _, object := range aliases {
				t.Cleanup(func() { os.Remove(object) })
			}
			if err != nil || len(aliases) != 1 {
				t.Fatalf("actual native alias object: %v, %v", aliases, err)
			}
			object, err := macho.Open(aliases[0])
			if err != nil {
				t.Fatal(err)
			}
			defer object.Close()
			symbols := make(map[string]bool)
			for _, symbol := range object.Symtab.Syms {
				symbols[symbol.Name] = true
			}
			for _, kind := range []string{"word", "float", "pair"} {
				for _, symbol := range []string{"_imported_" + kind,
					"_imported_" + kind + "_trampoline", "_imported_" + kind + "_trampoline_addr",
					"_fixture_native_" + kind} {
					if !symbols[symbol] {
						t.Errorf("native object lacks exact symbol %s", symbol)
					}
				}
				checkDarwinTrampolineRelocation(t, object, "_fixture_native_"+kind, 2, true)
				checkDarwinTrampolineRelocation(t, object, "_imported_"+kind+"_trampoline", 3, false)
			}
			if signatures, err := plan9asmSigsForPkg(ctx, pkg.PkgPath); err != nil || len(signatures) != 0 {
				t.Fatalf("native entries must not acquire ordinary Go signatures: %v, %v", signatures, err)
			}
			if strings.Contains(pkg.LPkg.Module().String(), "call void @imported_") {
				t.Fatal("foreign machine call was projected to a guessed void prototype")
			}
			if runtime.GOOS == "darwin" && runtime.GOARCH == arch {
				checkDarwinTrampolineRuntime(t, aliases[0])
			}
		})
	}
}

func checkDarwinTrampolineRelocation(t *testing.T, object *macho.File, target string, width uint8, pcrel bool) {
	t.Helper()
	for _, section := range object.Sections {
		for _, relocation := range section.Relocs {
			if relocation.Extern && int(relocation.Value) < len(object.Symtab.Syms) && object.Symtab.Syms[relocation.Value].Name == target && relocation.Len == width && relocation.Pcrel == pcrel {
				return
			}
		}
	}
	t.Fatalf("native object lacks exact relocation to %s (width=%d, PC-relative=%t)", target, width, pcrel)
}

func checkDarwinTrampolineRuntime(t *testing.T, object string) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "native.c")
	if err := os.WriteFile(source, []byte(`
#include <stdint.h>
#include <stdio.h>
typedef struct { uint64_t first, second; } pair;
uint64_t fixture_native_word(uint64_t a,uint64_t b,uint64_t c,uint64_t d,uint64_t e,uint64_t f,uint64_t g,uint64_t h) {
 return a+2*b+3*c+4*d+5*e+6*f+7*g+8*h;
}
double fixture_native_float(double a,double b,double c,double d,double e,double f,double g,double h,double i,double j) {
 return a+2*b+3*c+4*d+5*e+6*f+7*g+8*h+9*i+10*j;
}
pair fixture_native_pair(uint64_t a,uint64_t b) { pair p={a+3,b+7}; return p; }
extern uint64_t imported_word_trampoline(uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t);
extern double imported_float_trampoline(double,double,double,double,double,double,double,double,double,double);
extern pair imported_pair_trampoline(uint64_t,uint64_t);
int main(void) {
 pair p=imported_pair_trampoline(11,23);
 if(imported_word_trampoline(1,2,3,4,5,6,7,8)!=204 ||
    imported_float_trampoline(1,2,3,4,5,6,7,8,9,10)!=385 || p.first!=14 || p.second!=30) return 1;
 puts("native trampoline arguments and results verified");
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "native")
	command := abi0FixtureCommand(t, time.Minute, "clang", "-O2", source, object, "-o", binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native trampoline C link: %v\n%s", err, output)
	}
	output, err := abi0FixtureCommand(t, 10*time.Second, binary).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "native trampoline arguments and results verified" {
		t.Fatalf("native trampoline C execution: %v\n%s", err, output)
	}
}

func TestDarwinDynimportTrampolineSourceOwnership(t *testing.T) {
	base := dynimportTrampolineFiles()
	for _, row := range []struct {
		name, goSource, assembly string
	}{
		{"missing pragma", strings.Replace(base["main.go"], "//go:cgo_import_dynamic imported_word fixture_native_word \"/usr/lib/libSystem.B.dylib\"", "", 1), base["entry.s"]},
		{"mismatched pragma", strings.Replace(base["main.go"], "dynamic imported_word ", "dynamic another_word ", 1), base["entry.s"]},
		{"no native alias object", strings.Replace(base["main.go"], "imported_word fixture_native_word", "imported_word imported_word", 1), base["entry.s"]},
		{"changed branch", base["main.go"], strings.Replace(base["entry.s"], "JMP imported_word(SB)", "JMP unknown_word(SB)", 1)},
		{"offset branch", base["main.go"], strings.Replace(base["entry.s"], "JMP imported_word(SB)", "JMP imported_word+1(SB)", 1)},
		{"side effect", base["main.go"], strings.Replace(base["entry.s"], "JMP imported_word(SB)", "MOVD $7, R0\n JMP imported_word(SB)", 1)},
		{"extra return", base["main.go"], base["entry.s"] + "RET\n"},
		{"TEXT flags", base["main.go"], strings.Replace(base["entry.s"], "NOSPLIT", "NOSPLIT|NOFRAME", 1)},
		{"TEXT frame", base["main.go"], strings.Replace(base["entry.s"], "$0-0", "$8-0", 1)},
		{"TEXT arguments", base["main.go"], strings.Replace(base["entry.s"], "$0-0", "$0-8", 1)},
		{"typed Go call", base["main.go"] + "func use(){ imported_word_trampoline() }\n", base["entry.s"]},
		{"Go function value", base["main.go"] + "var use = imported_word_trampoline\n", base["entry.s"]},
		{"Go body", strings.Replace(base["main.go"], "func imported_word_trampoline()", "func imported_word_trampoline() {}", 1), base["entry.s"]},
		{"Go arguments", strings.Replace(base["main.go"], "func imported_word_trampoline()", "func imported_word_trampoline(x uintptr)", 1), base["entry.s"]},
		{"renamed Go declaration", strings.Replace(base["main.go"], "func imported_word_trampoline()", "//go:linkname imported_word_trampoline other.symbol\nfunc imported_word_trampoline()", 1), base["entry.s"]},
		{"native declaration", strings.Replace(base["main.go"], "func imported_word_trampoline()", "//llgo:cdecl\nfunc imported_word_trampoline()", 1), base["entry.s"]},
	} {
		t.Run(row.name, func(t *testing.T) {
			files := map[string]string{"go.mod": base["go.mod"], "main.go": row.goSource, "entry.s": row.assembly}
			ctx, pkg := nativeAddressFixturePackageAtPath(t, files, "example.com/nativebridge", "darwin", "arm64", "arm64-apple-darwin")
			sources, err := captureSelectedPlan9Asm(ctx, pkg.Package, []string{filepath.Join(pkg.Dir, "entry.s")})
			if err == nil && (len(sources) != 1 || sources[0].dynimport) {
				t.Fatalf("source without complete native ownership was claimed: %+v", sources)
			}
		})
	}
	t.Run("duplicate source", func(t *testing.T) {
		ctx, pkg := nativeAddressFixturePackageAtPath(t, base, "example.com/nativebridge", "darwin", "arm64", "arm64-apple-darwin")
		file := filepath.Join(pkg.Dir, "entry.s")
		if _, err := captureSelectedPlan9Asm(ctx, pkg.Package, []string{file, file}); err == nil {
			t.Fatal("duplicate native ownership accepted")
		}
	})
	t.Run("ambiguous declaration", func(t *testing.T) {
		ctx, pkg := nativeAddressFixturePackageAtPath(t, base, "example.com/nativebridge", "darwin", "arm64", "arm64-apple-darwin")
		for _, declaration := range pkg.Syntax[0].Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "imported_word_trampoline" {
				pkg.Syntax[0].Decls = append(pkg.Syntax[0].Decls, declaration)
				break
			}
		}
		sources, err := captureSelectedPlan9Asm(ctx, pkg.Package, []string{filepath.Join(pkg.Dir, "entry.s")})
		if err != nil || sources[0].dynimport {
			t.Fatalf("ambiguous native declaration: %v, %v", sources, err)
		}
	})
}

func TestDarwinDynimportTrampolineAddressOwnership(t *testing.T) {
	goSource := `package nativebridge
//go:cgo_import_dynamic imported_word fixture_native_word "/usr/lib/libSystem.B.dylib"
var imported_word_trampoline_addr uintptr
`
	assembly := `#include "textflag.h"
TEXT imported_word_trampoline<>(SB),NOSPLIT,$0-0
 JMP imported_word(SB)
GLOBL ·imported_word_trampoline_addr(SB),RODATA,$8
DATA ·imported_word_trampoline_addr(SB)/8,$imported_word_trampoline<>(SB)
`
	for _, row := range []struct {
		name, goSource, assembly string
		wantOwned                bool
	}{
		{"complete address slot", goSource, assembly, true},
		{"address read", goSource + "func pc() uintptr { return imported_word_trampoline_addr }\n", assembly, true},
		{"address write", goSource + "func mutate(){ imported_word_trampoline_addr=7 }\n", assembly, false},
		{"address escape", goSource + "var escape = &imported_word_trampoline_addr\n", assembly, false},
		{"initialized address", strings.Replace(goSource, "addr uintptr", "addr uintptr = 7", 1), assembly, false},
		{"duplicate DATA", goSource, assembly + "DATA ·imported_word_trampoline_addr(SB)/8,$imported_word_trampoline<>(SB)\n", false},
		{"duplicate GLOBL", goSource, assembly + "GLOBL ·imported_word_trampoline_addr(SB),RODATA,$8\n", false},
		{"wrong DATA target", goSource, strings.Replace(assembly, "$imported_word_trampoline<>(SB)", "$unknown<>(SB)", 1), false},
		{"writable GLOBL", goSource, strings.Replace(assembly, "RODATA", "NOPTR", 1), false},
	} {
		t.Run(row.name, func(t *testing.T) {
			files := map[string]string{"go.mod": "module example.com/nativebridge\n\ngo 1.20\n", "main.go": row.goSource, "entry.s": row.assembly}
			ctx, pkg := nativeAddressFixturePackageAtPath(t, files, "example.com/nativebridge", "darwin", "arm64", "arm64-apple-darwin")
			sources, err := captureSelectedPlan9Asm(ctx, pkg.Package, []string{filepath.Join(pkg.Dir, "entry.s")})
			if err != nil || sources[0].dynimport != row.wantOwned {
				t.Fatalf("source ownership=%v, want %t; err=%v", sources, row.wantOwned, err)
			}
		})
	}
}

func TestDarwinDynimportTrampolineSDK(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, source := range []struct {
			path, file string
			count      int
		}{
			{"internal/syscall/unix", "asm_darwin.s", 26},
			{"golang.org/x/sys/unix", "zsyscall_darwin_" + arch + ".s", 159},
		} {
			t.Run(source.path+"/"+arch, func(t *testing.T) {
				loaded, err := gopackages.Load(&gopackages.Config{
					Mode: gopackages.LoadSyntax,
					Env:  withEnv(os.Environ(), "GOROOT="+runtime.GOROOT(), "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOPROXY=off", "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0"),
					ParseFile: func(fset *token.FileSet, filename string, contents []byte) (*ast.File, error) {
						return parser.ParseFile(fset, filename, contents, parser.ParseComments)
					},
				}, source.path)
				if err != nil || len(loaded) != 1 || gopackages.PrintErrors(loaded) != 0 {
					t.Fatalf("actual selected SDK declarations: %v", err)
				}
				pkg := loaded[0]
				prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: arch})
				defer prog.Dispose()
				current := &aPackage{Package: pkg, LPkg: prog.NewPackage(pkg.Name, pkg.PkgPath)}
				current.ExportFile = filepath.Join(t.TempDir(), "sdk.a")
				triple := "arm64-apple-darwin"
				if arch == "amd64" {
					triple = "x86_64-apple-darwin"
				}
				ctx, _ := nativeAddressFixturePackageAtPath(t, dynimportTrampolineFiles(), "example.com/nativebridge", "darwin", arch, triple)
				ctx.prog = prog
				ctx.pkgs = map[*packages.Package]Package{pkg: {Package: pkg}}
				file := filepath.Join(filepath.Dir(pkg.GoFiles[0]), source.file)
				sources, err := captureSelectedPlan9Asm(ctx, pkg, []string{file})
				if err != nil || len(sources) != 1 || !sources[0].dynimport {
					t.Fatalf("complete actual SDK trampoline ownership: %v, %v", sources, err)
				}
				if count := strings.Count(string(sources[0].source), "TEXT "); count != source.count {
					t.Fatalf("actual source trampoline inventory=%d, want %d", count, source.count)
				}
				objects, err := compileSelectedPkgSFiles(ctx, current, pkg, []string{file}, false)
				if err != nil || len(objects) != 0 {
					t.Fatalf("actual SDK acquired duplicate typed objects: %v, %v", objects, err)
				}
				objects, err = buildGoCgoAliasObjects(ctx, pkg.PkgPath, pkg.Syntax, false)
				for _, object := range objects {
					t.Cleanup(func() { os.Remove(object) })
				}
				if err != nil || len(objects) != 1 {
					t.Fatalf("actual SDK native producer: %v, %v", objects, err)
				}
				object, err := macho.Open(objects[0])
				if err != nil {
					t.Fatal(err)
				}
				defer object.Close()
				_, imports := collectGoCgoPragmas(pkg.Syntax)
				for _, imp := range imports {
					if strings.Contains(string(sources[0].source), "·"+imp.local+"_trampoline(SB)") || strings.Contains(string(sources[0].source), imp.local+"_trampoline<>(SB)") {
						checkDarwinTrampolineRelocation(t, object, "_"+imp.alias, 2, true)
					}
				}
				t.Logf("complete selected source %s: %d native trampoline entries", file, source.count)
			})
		}
	}
}
