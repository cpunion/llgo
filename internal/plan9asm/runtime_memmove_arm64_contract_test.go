package plan9asm

import (
	"context"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xgo-dev/llvm"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestRuntimeMemmoveARM64ContractMatchesActualGoDeclaration(t *testing.T) {
	// Reuse the actual function AST and unsafe import from this toolchain's
	// runtime source. Do not manufacture a function to satisfy Derive's check.
	fset := token.NewFileSet()
	path := filepath.Join(runtime.GOROOT(), "src/runtime/stubs.go")
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declaration *ast.FuncDecl
	var unsafeImport *ast.ImportSpec
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "memmove" {
			declaration = fn
		}
	}
	for _, imp := range file.Imports {
		if imp.Path.Value == `"unsafe"` {
			unsafeImport = imp
		}
	}
	if declaration == nil || declaration.Body != nil || unsafeImport == nil {
		t.Fatal("actual runtime assembly declaration or unsafe import missing")
	}
	selected := &ast.File{Name: file.Name, Decls: []ast.Decl{
		&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{unsafeImport}}, declaration,
	}, Imports: []*ast.ImportSpec{unsafeImport}}
	config := types.Config{Importer: importer.Default(), Sizes: types.SizesFor("gc", "arm64")}
	pkg, err := config.Check("runtime", fset, []*ast.File{selected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := extraAsmSigsAndDeclMap("example.com/copy", "arm64")["runtime.memmove"]
	want, err := extplan9asm.DeriveARM64GoRegisterABI(pkg.Scope().Lookup("memmove").(*types.Func), sig)
	if err != nil {
		t.Fatal(err)
	}
	if sig.ARM64GoRegisterABI == nil || !reflect.DeepEqual(sig.ARM64GoRegisterABI, want) {
		t.Fatalf("runtime memmove ARM64 entry = %+v, actual Go declaration derives %+v", sig.ARM64GoRegisterABI, want)
	}
	for _, arch := range []string{"amd64", "386", "arm", "wasm"} {
		if extraAsmSigsAndDeclMap("example.com/copy", arch)["runtime.memmove"].ARM64GoRegisterABI != nil {
			t.Errorf("ARM64 register contract leaked to %s", arch)
		}
	}
}

func runtimeMemmoveARM64FixtureSource(t *testing.T) string {
	t.Helper()
	// Read the unchanged fixture's literal, rather than maintaining a second
	// assembly implementation that could drift from the regression source.
	file, err := parser.ParseFile(token.NewFileSet(), "runtime_helper_abi_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasPrefix(value, "TEXT ABI0Copy(SB)") &&
			strings.Contains(value, "MOVD dst+0(FP)") && strings.Contains(value, "TEXT InternalCopy(SB)") {
			source = value
		}
		return true
	})
	if source == "" {
		t.Fatal("unchanged ARM64 memmove fixture literal missing")
	}
	return source
}

func TestRuntimeMemmoveARM64MissingOrMalformedEntryRemainsContext(t *testing.T) {
	fixture := runtimeMemmoveARM64FixtureSource(t)
	source := fixture[strings.Index(fixture, "TEXT InternalCopy(SB)"):]
	file, err := extplan9asm.Parse(extplan9asm.ArchARM64, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct {
		name   string
		change func(*extplan9asm.FuncSig)
	}{
		{"absent", func(sig *extplan9asm.FuncSig) { sig.ARM64GoRegisterABI = nil }},
		{"incomplete", func(sig *extplan9asm.FuncSig) { sig.ARM64GoRegisterABI.Params = sig.ARM64GoRegisterABI.Params[:2] }},
		{"wrong_register", func(sig *extplan9asm.FuncSig) { sig.ARM64GoRegisterABI.Params[2].Register = "R3" }},
		{"wrong_type", func(sig *extplan9asm.FuncSig) { sig.ARM64GoRegisterABI.Params[0].Type = extplan9asm.I64 }},
	} {
		t.Run(edit.name, func(t *testing.T) {
			for _, triple := range []string{"aarch64-unknown-linux-gnu", "aarch64-unknown-linux-musl", "aarch64-apple-darwin", "aarch64-pc-windows-msvc"} {
				sig := extraAsmSigsAndDeclMap("example.com/copy", "arm64")["runtime.memmove"]
				if sig.ARM64GoRegisterABI == nil {
					t.Fatal("positive contract missing before mutation")
				}
				edit.change(&sig)
				opt := extplan9asm.Options{Goarch: "arm64", TargetTriple: triple,
					ResolveSym: func(sym string) string { return strings.ReplaceAll(StripABISuffix(sym), "·", ".") },
					Sigs: map[string]extplan9asm.FuncSig{
						"InternalCopy":    {Name: "InternalCopy", Args: sig.Args, Ret: extplan9asm.Void, Frame: sig.Frame},
						"runtime.memmove": sig,
					},
				}
				if _, err := extplan9asm.Translate(file, opt); !errors.Is(err, extplan9asm.ErrProbeNeedsContext) {
					t.Errorf("%s missing/malformed contract accepted: %v", triple, err)
				}
				ctx := llvm.NewContext()
				module, err := extplan9asm.TranslateModuleInContext(ctx, file, opt)
				if err == nil {
					module.Dispose()
				}
				ctx.Dispose()
				if !errors.Is(err, extplan9asm.ErrProbeNeedsContext) {
					t.Errorf("%s module missing/malformed contract accepted: %v", triple, err)
				}
			}
		})
	}
}

func runtimeMemmoveARM64CrossTools(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getenv("PLAN9ASM_CROSS_EXEC") != "1" {
		return nil
	}
	for _, tool := range []string{"aarch64-linux-gnu-gcc", "qemu-aarch64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required ARM64 cross-runtime tool %s: %v", tool, err)
		}
	}
	return []string{"qemu-aarch64", "-L", "/usr/aarch64-linux-gnu"}
}

func TestRuntimeMemmoveARM64ActualGoRegisterCall(t *testing.T) {
	runner := runtimeMemmoveARM64CrossTools(t)
	if (runtime.GOARCH != "arm64" || runtime.GOOS != "linux" && runtime.GOOS != "darwin") && len(runner) == 0 {
		t.Skip("ARM64 native execution or required Linux ARM64 cross-runtime counterpart")
	}
	fixture := runtimeMemmoveARM64FixtureSource(t)
	start := strings.Index(fixture, "TEXT InternalCopy(SB)")
	if start < 0 {
		t.Fatal("original InternalCopy source missing")
	}
	// Qualify only the Go entry symbol; retain every original instruction,
	// frame, deliberately wrong ABI0 slot and explicit runtime call selector.
	source := "#include \"textflag.h\"\n" + strings.Replace(fixture[start:], "TEXT InternalCopy(SB)", "TEXT main·InternalCopy(SB)", 1)
	dir := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":       "module memmoveentryoracle\n\ngo 1.27\n",
		"copy_arm64.s": source,
		"main.go": `package main
import "unsafe"
func InternalCopy(dst,src unsafe.Pointer,n uintptr)
func main() {
 for _,dst:=range []int{0,1,9,17,31} { for _,src:=range []int{0,1,9,17,31} { for _,n:=range []int{0,1,2,3,8,17,32,33} {
  var data,want [96]byte
  for i:=range data { data[i]=byte(i*37+dst*7+src*11+n); want[i]=data[i] }
  var snapshot [33]byte
  for i:=0;i<n;i++ { snapshot[i]=data[src+i] }
  for i:=0;i<n;i++ { want[dst+i]=snapshot[i] }
  InternalCopy(unsafe.Pointer(&data[dst]),unsafe.Pointer(&data[src]),uintptr(n))
  if data!=want { panic("actual Go memmove ABIInternal/canary mismatch") }
 } } }
}

`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"run", "-p=2", "-asmflags=memmoveentryoracle=-p=runtime"}
	env := append(os.Environ(), "GOARCH=arm64", "CGO_ENABLED=0")
	if len(runner) != 0 {
		args = append(args, "-exec=qemu-aarch64")
		env = append(env, "GOOS=linux")
	}
	args = append(args, ".")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Env = dir, env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual Go original ABIInternal memmove source: %v\n%s", err, out)
	}
	t.Log("actual Go original InternalCopy passed all 200 overlap/unaligned/full-canary vectors")
}

func TestRuntimeMemmoveARM64OriginalFixtureObjectsAndRuntime(t *testing.T) {
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		config = "llvm-config"
	}
	version, err := exec.Command(config, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(version)), "22.") {
		t.Fatalf("LLVM 22 required: %s, %v", version, err)
	}
	bindir, err := exec.Command(config, "--bindir").Output()
	if err != nil {
		t.Fatal(err)
	}
	clang := filepath.Join(strings.TrimSpace(string(bindir)), "clang")
	runner := runtimeMemmoveARM64CrossTools(t)
	source := runtimeMemmoveARM64FixtureSource(t)
	file, err := extplan9asm.Parse(extplan9asm.ArchARM64, source)
	if err != nil {
		t.Fatal(err)
	}
	memmove := extraAsmSigsAndDeclMap("example.com/copy", "arm64")["runtime.memmove"]
	resultFrame := memmove.Frame
	resultFrame.Results = []extplan9asm.FrameSlot{{Offset: 24, Type: extplan9asm.Ptr, Index: 0, Field: -1}}
	sigs := map[string]extplan9asm.FuncSig{
		"ABI0Copy":        {Name: "ABI0Copy", Args: memmove.Args, Ret: extplan9asm.Void, Frame: memmove.Frame},
		"InternalCopy":    {Name: "InternalCopy", Args: memmove.Args, Ret: extplan9asm.Void, Frame: memmove.Frame},
		"RegisterCopy":    {Name: "RegisterCopy", Args: memmove.Args, Ret: extplan9asm.Ptr, Frame: resultFrame},
		"runtime.memmove": memmove,
		"memmove":         {Name: "memmove_register", Args: memmove.Args, Ret: extplan9asm.Ptr, ArgRegs: []extplan9asm.Reg{"R0", "R1", "R2"}},
	}
	for _, target := range []struct{ goos, triple string }{
		{"linux", "aarch64-unknown-linux-gnu"}, {"linux", "aarch64-unknown-linux-musl"},
		{"darwin", "aarch64-apple-darwin"}, {"windows", "aarch64-pc-windows-msvc"},
	} {
		t.Run(target.triple, func(t *testing.T) {
			ir, err := extplan9asm.Translate(file, extplan9asm.Options{Goarch: "arm64", TargetTriple: target.triple, Sigs: sigs,
				ResolveSym: func(sym string) string { return strings.ReplaceAll(StripABISuffix(sym), "·", ".") },
			})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			ll, object := filepath.Join(dir, "copy.ll"), filepath.Join(dir, "copy.o")
			if err := os.WriteFile(ll, []byte(ir), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(clang, "-target", target.triple, "-O2", "-c", ll, "-o", object).CombinedOutput(); err != nil {
				t.Fatalf("LLVM 22 original fixture object: %v\n%s", err, out)
			}
			native := runtime.GOARCH == "arm64" && target.goos == runtime.GOOS
			cross := len(runner) != 0 && target.goos == "linux"
			if (!native && !cross) || strings.Contains(target.triple, "musl") {
				t.Log("LLVM object only; required runtime is the native or Linux ARM64 counterpart")
				return
			}
			driver, binary := filepath.Join(dir, "driver.c"), filepath.Join(dir, "driver")
			if err := os.WriteFile(driver, []byte(runtimeMemmoveARM64CanaryDriver), 0600); err != nil {
				t.Fatal(err)
			}
			compiler := clang
			if cross {
				compiler = "aarch64-linux-gnu-gcc"
			}
			if out, err := exec.Command(compiler, driver, object, "-o", binary).CombinedOutput(); err != nil {
				t.Fatalf("link original fixture canary oracle: %v\n%s", err, out)
			}
			cmd := exec.Command(binary)
			if cross {
				cmd = exec.Command(runner[0], append(runner[1:], binary)...)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("execute original fixture canary oracle: %v\n%s", err, out)
			}
			t.Log("all three unchanged copy functions passed 600 overlap/unaligned/full-canary vectors")
		})
	}
}

const runtimeMemmoveARM64CanaryDriver = `#include <stdint.h>
#include <string.h>
extern void ABI0Copy(void *,const void *,uint64_t);
extern void InternalCopy(void *,const void *,uint64_t);
extern void *RegisterCopy(void *,const void *,uint64_t);
void *memmove_register(void *dst,const void *src,uint64_t n) { return memmove(dst,src,n); }
int main(void) {
 const int offsets[]={0,1,9,17,31},sizes[]={0,1,2,3,8,17,32,33};
 for(int f=0;f<3;f++) for(int d=0;d<5;d++) for(int s=0;s<5;s++) for(int z=0;z<8;z++) {
  unsigned char data[96],want[96],snapshot[33];
  int dst=offsets[d],src=offsets[s],n=sizes[z];
  for(int i=0;i<96;i++) data[i]=want[i]=(unsigned char)(i*37+dst*7+src*11+n);
  for(int i=0;i<n;i++) snapshot[i]=data[src+i];
  for(int i=0;i<n;i++) want[dst+i]=snapshot[i];
  if(f==0) ABI0Copy(data+dst,data+src,(uint64_t)n);
  else if(f==1) InternalCopy(data+dst,data+src,(uint64_t)n);
  else if(RegisterCopy(data+dst,data+src,(uint64_t)n)!=data+dst) return 2;
  for(int i=0;i<96;i++) if(data[i]!=want[i]) return 1;
 }
 return 0;
}
`
