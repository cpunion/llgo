//go:build !llgo

package build

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestELFFullLTOAlignmentMachineCode(t *testing.T) {
	t.Setenv("CCFLAGS", "")
	t.Setenv("LDFLAGS", "")
	dir := t.TempDir()
	source := filepath.Join(dir, "loop.c")
	const code = `
__attribute__((noinline)) unsigned long leaf(volatile unsigned long *p) { return *p; }
unsigned long sum(volatile unsigned long *p, unsigned long n) {
    unsigned long result = 0;
    for (unsigned long i = 0; i < n; ++i) result += leaf(p + i);
    return result;
}
`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	bitcode := filepath.Join(dir, "loop.o")
	compile := exec.Command("clang", "--target=x86_64-unknown-linux-gnu", "-O3", "-fno-unroll-loops", "-flto=full", "-c", source, "-o", bitcode)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile LTO fixture: %v\n%s", err, output)
	}
	for _, size := range []int{16, 16 + 0x390} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			assembly := filepath.Join(dir, fmt.Sprintf("metadata-%d.s", size))
			padding := filepath.Join(dir, fmt.Sprintf("metadata-%d.o", size))
			text := fmt.Sprintf(".section .rodata.llgo_layout,\"a\",@progbits\n.globl layout_padding\nlayout_padding:\n.zero %d\n", size)
			if err := os.WriteFile(assembly, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			compile := exec.Command("clang", "--target=x86_64-unknown-linux-gnu", "-c", assembly, "-o", padding)
			if output, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile metadata fixture: %v\n%s", err, output)
			}
			ctx := elfLTOContext()
			ctx.crossCompile.LDFLAGS = []string{
				"--target=x86_64-unknown-linux-gnu", "-fuse-ld=lld", "-flto=full", "-O3", "-Wl,--lto-O3",
				"-nostdlib", "-Wl,-e,sum", "-Wl,--undefined=layout_padding", "-Wl,--undefined=leaf",
			}
			binary := filepath.Join(dir, fmt.Sprintf("layout-%d", size))
			if err := ctx.linker().Link("-o", binary, bitcode, padding); err != nil {
				t.Fatal(err)
			}
			file, err := elf.Open(binary)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			symbols, err := file.Symbols()
			if err != nil {
				t.Fatal(err)
			}
			var sum elf.Symbol
			for _, symbol := range symbols {
				if symbol.Name == "leaf" || symbol.Name == "sum" {
					if symbol.Value%64 != 0 {
						t.Errorf("%s entry %#x is not cache-line aligned", symbol.Name, symbol.Value)
					}
				}
				if symbol.Name == "sum" {
					sum = symbol
				}
			}
			if sum.Size == 0 {
				t.Fatal("loop function was not retained")
			}
			disassemble := exec.Command("llvm-objdump", "-d", "--no-show-raw-insn", "--disassemble-symbols=sum", binary)
			output, err := disassemble.CombinedOutput()
			if err != nil {
				t.Fatalf("disassemble loop: %v\n%s", err, output)
			}
			branches := regexp.MustCompile(`(?m)^\s*([0-9a-f]+):\s+j[a-z]+\s+0x([0-9a-f]+)`)
			loops := 0
			for _, match := range branches.FindAllSubmatch(output, -1) {
				pc, err := strconv.ParseUint(string(match[1]), 16, 64)
				if err != nil {
					t.Fatal(err)
				}
				target, err := strconv.ParseUint(string(match[2]), 16, 64)
				if err != nil {
					t.Fatal(err)
				}
				if target >= sum.Value && target < pc {
					loops++
					if target%32 != 0 {
						t.Errorf("loop target %#x is not 32-byte aligned", target)
					}
				}
			}
			if loops == 0 {
				t.Fatal("fixture has no backward loop branch")
			}
		})
	}
}
