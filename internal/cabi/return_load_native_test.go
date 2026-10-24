//go:build !llgo && (linux || darwin)

package cabi

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// Link the actual transformed function with a C caller so that frontend
// optimizations cannot remove the aggregate load before ABI conversion.
func TestAggregateReturnCarrierAtProtectedBoundary(t *testing.T) {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		t.Skip("native ABI fixture supports arm64 and amd64")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("native ABI fixture requires clang")
	}
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllAsmPrinters()
	for _, size := range []int{9, 12, 15} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes%d/optimize%t", size, optimize), func(t *testing.T) {
				prog := llssa.NewProgram(&llssa.Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
				defer prog.Dispose()
				ctx := llvm.NewContext()
				defer ctx.Dispose()
				m := ctx.NewModule("return_boundary")
				defer m.Dispose()
				machine := prog.TargetMachine()
				m.SetTarget(machine.Triple())
				m.SetDataLayout(prog.TargetData().String())
				typ := llvm.ArrayType(ctx.Int8Type(), size)
				fn := llvm.AddFunction(m, "read_value", llvm.FunctionType(typ, []llvm.Type{llvm.PointerType(typ, 0)}, false))
				b := ctx.NewBuilder()
				defer b.Dispose()
				b.SetInsertPointAtEnd(ctx.AddBasicBlock(fn, "entry"))
				value := b.CreateLoad(typ, fn.Param(0), "captured")
				value.SetAlignment(1)
				b.CreateRet(value)
				NewTransformer(prog, machine.Triple(), "", ModeAllFunc, optimize).TransformModule("test", m)
				if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err, m.String())
				}
				options := llvm.NewPassBuilderOptions()
				err := m.RunPasses("default<Os>", machine, options)
				options.Dispose()
				if err != nil {
					t.Fatal(err)
				}
				object, err := machine.EmitToMemoryBuffer(m, llvm.ObjectFile)
				if err != nil {
					t.Fatal(err)
				}
				defer object.Dispose()
				dir := t.TempDir()
				objPath, srcPath, image := filepath.Join(dir, "return.o"), filepath.Join(dir, "main.c"), filepath.Join(dir, "boundary")
				if err := os.WriteFile(objPath, object.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				const caller = `#include <sys/mman.h>
#include <unistd.h>
typedef struct { unsigned char bytes[%d]; } Value;
extern Value read_value(const unsigned char *);
int main(void) {
  long page = sysconf(_SC_PAGESIZE);
  if (page <= 0) return 10;
  unsigned char *data = mmap(0, 2*page, PROT_READ|PROT_WRITE, MAP_ANON|MAP_PRIVATE, -1, 0);
  if (data == MAP_FAILED) return 11;
  if (mprotect(data+page, page, PROT_NONE)) return 12;
  for (int i = 0; i < %d; ++i) data[page-%d+i] = (unsigned char)(31+i);
  Value value = read_value(data+page-%d);
  for (int i = 0; i < %d; ++i) if (value.bytes[i] != 31+i) return 13;
  return munmap(data, 2*page) ? 14 : 0;
}
`
				if err := os.WriteFile(srcPath, []byte(fmt.Sprintf(caller, size, size, size, size, size)), 0o644); err != nil {
					t.Fatal(err)
				}
				if output, err := exec.Command(clang, "-O0", srcPath, objPath, "-o", image).CombinedOutput(); err != nil {
					t.Fatalf("link ABI boundary: %v\n%s", err, output)
				}
				if output, err := exec.Command(image).CombinedOutput(); err != nil {
					t.Fatalf("run ABI boundary: %v\n%s\n%s", err, output, m.String())
				}
			})
		}
	}
}
