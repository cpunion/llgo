package plan9asm

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestARM64BytealgPrivateRegisterSignatures(t *testing.T) {
	for _, helper := range []struct {
		name string
		regs []extplan9asm.Reg
	}{
		{"cmpbody", []extplan9asm.Reg{"R0", "R1", "R2", "R3"}},
		{"memeqbody", []extplan9asm.Reg{"R0", "R1", "R2"}},
	} {
		sig := extraAsmSigsAndDeclMap("internal/bytealg", "arm64")["internal/bytealg."+helper.name]
		if !reflect.DeepEqual(sig.ArgRegs, helper.regs) || len(sig.Frame.Params) != 0 || len(sig.Frame.Results) != 0 {
			t.Errorf("private %s register entry = %+v, want %v without a guessed Go FP frame", helper.name, sig, helper.regs)
		}
	}
}

// Compile the unchanged standard-library source, including both declared
// ABIInternal tails and the private helper. Execute only cmpbody's documented
// scalar register boundary here: LLVM aggregate/C ABI rewrites are exercised
// separately by the package-level E2E tests.
func TestARM64BytealgCompareSourceObjectAndRuntime(t *testing.T) {
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
	source := filepath.Join(runtime.GOROOT(), "src", "internal", "bytealg", "compare_arm64.s")
	dir := t.TempDir()
	for _, target := range []struct{ goos, triple string }{
		{"linux", "aarch64-unknown-linux-gnu"},
		{"linux", "aarch64-unknown-linux-musl"},
		{"darwin", "aarch64-apple-darwin"},
		{"windows", "aarch64-pc-windows-msvc"},
	} {
		t.Run(target.triple, func(t *testing.T) {
			pkg := loadStdlibInternalBytealgForTarget(t, target.goos, "arm64")
			translated, err := TranslateFileForPkg(pkg, source, target.goos, "arm64", nil)
			if err != nil {
				t.Fatal(err)
			}
			ll := filepath.Join(dir, target.triple+".ll")
			object := filepath.Join(dir, target.triple+".o")
			if err := os.WriteFile(ll, []byte(translated.LLVMIR), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(clang, "-target", target.triple, "-O2", "-c", ll, "-o", object).CombinedOutput(); err != nil {
				t.Fatalf("compile actual source: %v\n%s", err, out)
			}
			if runtime.GOARCH != "arm64" || target.goos != runtime.GOOS || strings.Contains(target.triple, "musl") {
				t.Log("LLVM object only; no runtime claim for this target")
				return
			}
			driver := filepath.Join(dir, "driver.c")
			if err := os.WriteFile(driver, []byte(arm64CompareDriver), 0600); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "compare")
			if out, err := exec.Command(clang, driver, object, "-o", binary).CombinedOutput(); err != nil {
				t.Fatalf("link closed register helper: %v\n%s", err, out)
			}
			if out, err := exec.Command(binary).CombinedOutput(); err != nil {
				t.Fatalf("execute closed register helper: %v\n%s", err, out)
			}
		})
	}
}

const arm64CompareDriver = `#include <stdint.h>
extern int64_t cmpbody(const unsigned char *, int64_t, const unsigned char *, int64_t)
#ifdef __APPLE__
    __asm__("_internal/bytealg.cmpbody");
#else
    __asm__("internal/bytealg.cmpbody");
#endif

static int reference(const unsigned char *a, int n, const unsigned char *b, int m) {
    int end = n < m ? n : m;
    for (int i = 0; i < end; i++) {
        if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
    }
    return (n > m) - (n < m);
}

int main(void) {
    unsigned char a[260], b[260];
    for (int i = 0; i < 260; i++) a[i] = b[i] = (unsigned char)(i * 131 + 17);
    int sizes[] = {0,1,2,3,4,7,8,15,16,17,31,32,33,127,128,129,255};
    for (int offset = 0; offset < 4; offset++) {
        for (unsigned i = 0; i < sizeof(sizes)/sizeof(sizes[0]); i++) {
            int n = sizes[i];
            for (int m = n > 0 ? n-1 : 0; m <= n+1; m++) {
                if (cmpbody(a+offset,n,b+offset,m) != reference(a+offset,n,b+offset,m)) return 1;
            }
            for (int changed = 0; changed < n; changed++) {
                b[offset+changed] ^= 0x80;
                if (cmpbody(a+offset,n,b+offset,n) != reference(a+offset,n,b+offset,n)) return 2;
                if (cmpbody(b+offset,n,a+offset,n) != reference(b+offset,n,a+offset,n)) return 3;
                b[offset+changed] ^= 0x80;
            }
        }
    }
    return 0;
}
`
