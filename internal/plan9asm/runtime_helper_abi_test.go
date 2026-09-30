package plan9asm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestRuntimeMemmoveABI0Frame(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "arm", "wasm"} {
		t.Run(arch, func(t *testing.T) {
			sig := extraAsmSigsAndDeclMap("example.com/copy", arch)["runtime.memmove"]
			word := int64(8)
			lengthType := extplan9asm.I64
			if arch == "386" || arch == "arm" || arch == "wasm" {
				word = 4
				lengthType = extplan9asm.I32
			}
			if len(sig.Args) != 3 || sig.Args[2] != lengthType {
				t.Fatalf("memmove args = %v, want pointer-sized length %s", sig.Args, lengthType)
			}
			if len(sig.Frame.Params) != 3 {
				t.Fatalf("memmove ABI0 params = %#v, want three stack slots", sig.Frame.Params)
			}
			for i, typ := range sig.Args {
				slot := sig.Frame.Params[i]
				if slot.Offset != int64(i)*word || slot.Index != i || slot.Type != typ || slot.Field != -1 {
					t.Fatalf("memmove param %d = %#v", i, slot)
				}
			}
			// Go memmove has no result even though the C implementation returns dst.
			if sig.Ret != extplan9asm.Void || len(sig.Frame.Results) != 0 || len(sig.ArgRegs) != 0 {
				t.Fatalf("memmove result/register convention = %#v", sig)
			}
		})
	}
}

func TestRuntimeMemmoveStackAndRegisterCalls(t *testing.T) {
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		config = "llvm-config"
	}
	version, err := exec.Command(config, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(version)), "22.") {
		t.Fatalf("LLVM 22 required: %q, %v", version, err)
	}
	bindir, err := exec.Command(config, "--bindir").Output()
	if err != nil {
		t.Fatal(err)
	}
	clang := filepath.Join(strings.TrimSpace(string(bindir)), "clang")

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			var asm, triple string
			var registers []extplan9asm.Reg
			switch arch {
			case "arm64":
				triple = "aarch64-unknown-linux-gnu"
				registers = []extplan9asm.Reg{"R0", "R1", "R2"}
				asm = `TEXT ABI0Copy(SB),NOSPLIT,$32-24
	MOVD dst+0(FP), R5
	MOVD src+8(FP), R6
	MOVD n+16(FP), R7
	MOVD R5, 8(RSP)
	MOVD R6, 16(RSP)
	MOVD R7, 24(RSP)
	MOVD $1, R0
	MOVD $2, R1
	MOVD $3, R2
	CALL runtime·memmove(SB)
	RET
TEXT RegisterCopy(SB),NOSPLIT,$0-32
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2
	CALL memmove(SB)
	MOVD R0, ret+24(FP)
	RET
TEXT InternalCopy(SB),NOSPLIT,$32-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2
	MOVD $1, R5
	MOVD R5, 8(RSP)
	MOVD R5, 16(RSP)
	MOVD R5, 24(RSP)
	CALL runtime·memmove<ABIInternal>(SB)
	RET
`
			case "amd64":
				triple = "x86_64-unknown-linux-gnu"
				registers = []extplan9asm.Reg{extplan9asm.DI, extplan9asm.SI, extplan9asm.DX}
				asm = `TEXT ABI0Copy(SB),NOSPLIT,$24-24
	MOVQ dst+0(FP), R8
	MOVQ src+8(FP), R9
	MOVQ n+16(FP), R10
	MOVQ R8, 0(SP)
	MOVQ R9, 8(SP)
	MOVQ R10, 16(SP)
	MOVQ $1, AX
	MOVQ $2, BX
	MOVQ $3, CX
	CALL runtime·memmove(SB)
	RET
TEXT RegisterCopy(SB),NOSPLIT,$0-32
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), DX
	CALL memmove(SB)
	MOVQ AX, ret+24(FP)
	RET
TEXT InternalCopy(SB),NOSPLIT,$24-24
	MOVQ dst+0(FP), AX
	MOVQ src+8(FP), BX
	MOVQ n+16(FP), CX
	MOVQ $1, R8
	MOVQ R8, 0(SP)
	MOVQ R8, 8(SP)
	MOVQ R8, 16(SP)
	CALL runtime·memmove<ABIInternal>(SB)
	RET
`
			}
			if arch == runtime.GOARCH {
				hostTriple, err := exec.Command(clang, "-dumpmachine").Output()
				if err != nil {
					t.Fatal(err)
				}
				triple = strings.TrimSpace(string(hostTriple))
			}

			frame := extplan9asm.FrameLayout{Params: []extplan9asm.FrameSlot{
				{Offset: 0, Type: extplan9asm.Ptr, Index: 0, Field: -1},
				{Offset: 8, Type: extplan9asm.Ptr, Index: 1, Field: -1},
				{Offset: 16, Type: extplan9asm.I64, Index: 2, Field: -1},
			}}
			args := []extplan9asm.LLVMType{extplan9asm.Ptr, extplan9asm.Ptr, extplan9asm.I64}
			resultFrame := frame
			resultFrame.Results = []extplan9asm.FrameSlot{{Offset: 24, Type: extplan9asm.Ptr, Index: 0, Field: -1}}
			sigs := map[string]extplan9asm.FuncSig{
				"ABI0Copy":        {Name: "ABI0Copy", Args: args, Ret: extplan9asm.Void, Frame: frame},
				"RegisterCopy":    {Name: "RegisterCopy", Args: args, Ret: extplan9asm.Ptr, Frame: resultFrame},
				"InternalCopy":    {Name: "InternalCopy", Args: args, Ret: extplan9asm.Void, Frame: frame},
				"runtime.memmove": extraAsmSigsAndDeclMap("example.com/copy", arch)["runtime.memmove"],
				"memmove":         {Name: "memmove_register", Args: args, Ret: extplan9asm.Ptr, ArgRegs: registers},
			}
			file, err := extplan9asm.Parse(extplan9asm.Arch(arch), asm)
			if err != nil {
				t.Fatal(err)
			}
			ir, err := extplan9asm.Translate(file, extplan9asm.Options{
				Goarch: arch, TargetTriple: triple, Sigs: sigs,
				ResolveSym: func(sym string) string {
					return strings.ReplaceAll(StripABISuffix(sym), "·", ".")
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			assembly := filepath.Join(dir, "copy.s")
			if err := os.WriteFile(assembly, []byte("#include \"textflag.h\"\n"+asm), 0o644); err != nil {
				t.Fatal(err)
			}
			goAsm := exec.Command("go", "tool", "asm", "-gensymabis", "-p", "runtime",
				"-I", filepath.Join(runtime.GOROOT(), "pkg", "include"),
				"-o", filepath.Join(dir, "symabis"), assembly)
			goAsm.Env = append(os.Environ(), "GOARCH="+arch, "GOOS=linux")
			if output, err := goAsm.CombinedOutput(); err != nil {
				t.Fatalf("Go assembler rejects %s helper forms: %s\n%v", arch, output, err)
			}
			ll := filepath.Join(dir, "copy.ll")
			if err := os.WriteFile(ll, []byte(ir), 0o644); err != nil {
				t.Fatal(err)
			}
			object := filepath.Join(dir, "copy.o")
			if output, err := exec.Command(clang, "-target", triple, "-O2", "-c", ll, "-o", object).CombinedOutput(); err != nil {
				t.Fatalf("compile %s with LLVM 22: %s\n%v", arch, output, err)
			}
			if arch != runtime.GOARCH {
				t.Logf("LLVM 22 %s object compiled only; no execution on this host", arch)
				return
			}
			driver := filepath.Join(dir, "driver.c")
			if err := os.WriteFile(driver, []byte(memmoveCallDriver), 0o644); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "driver")
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			if output, err := exec.Command(clang, driver, object, "-o", binary).CombinedOutput(); err != nil {
				t.Fatalf("link native helper regression: %s\n%v", output, err)
			}
			if output, err := exec.Command(binary).CombinedOutput(); err != nil {
				t.Fatalf("run native stack/register helper regression: %s\n%v", output, err)
			}
		})
	}
}

const memmoveCallDriver = `#include <stdint.h>
#include <string.h>

extern void ABI0Copy(void *, const void *, uint64_t);
extern void InternalCopy(void *, const void *, uint64_t);
extern void *RegisterCopy(void *, const void *, uint64_t);

void *memmove_register(void *dst, const void *src, uint64_t n) {
    return memmove(dst, src, n);
}

int main(void) {
    unsigned char data[] = "abcdefghijklmno";
    ABI0Copy(data + 1, data, 10);
    if (memcmp(data, "aabcdefghij", 11) != 0) return 1;
    if (RegisterCopy(data + 2, data + 1, 9) != data + 2) return 2;
    if (memcmp(data, "aaabcdefghi", 11) != 0) return 3;
    unsigned char internal[8] = {0};
    InternalCopy(internal, "ABCDEFG", 7);
    if (memcmp(internal, "ABCDEFG", 8) != 0) return 4;
    return 0;
}
`
