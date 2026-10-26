//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

const runtimeLibraryAtomicTargetIR = `
declare i64 @foreign(ptr)
define i32 @add32(ptr %p, i32 %v) {
  %old = atomicrmw add ptr %p, i32 %v seq_cst, align 4
  ret i32 %old
}
define i64 @add64(ptr %p, i64 %v) {
  %old = atomicrmw add ptr %p, i64 %v seq_cst, align 8
  ret i64 %old
}
define { i64, i1 } @cas64(ptr %p, i64 %old, i64 %new) {
  %pair = cmpxchg ptr %p, i64 %old, i64 %new seq_cst seq_cst, align 8
  ret { i64, i1 } %pair
}
`

func parseRuntimeLibraryTargetTestIR(t *testing.T, prog llssa.Program, sources ...string) llvm.Module {
	t.Helper()
	file := filepath.Join(t.TempDir(), "atomics.ll")
	source := runtimeLibraryAtomicTargetIR
	if len(sources) == 1 {
		source = sources[0]
	}
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	buffer, err := llvm.NewMemoryBufferFromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx := prog.NewPackage("targetprobe", "example.com/targetprobe").Module().Context()
	mod, err := ctx.ParseIR(buffer)
	if err != nil {
		t.Fatal(err)
	}
	mod.SetTarget(prog.TargetSpec().Triple)
	mod.SetDataLayout(prog.DataLayout())
	return mod
}

// The native linker performs a separate full-LTO backend invocation. Cover
// that boundary with the same bitcode and C consumer in both cases, rather
// than treating a producer-side assembly test as a link-stage certificate.
func TestNativeRuntimeLTOTargetAttributes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native macOS ARM64 full-LTO linker gate")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	objdump, err := exec.LookPath("llvm-objdump")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, output)
		}
		return string(output)
	}
	sdk := strings.TrimSpace(run(t, "xcrun", "--sdk", "macosx", "--show-sdk-path"))
	llssa.Initialize(llssa.InitAll)
	source := strings.ReplaceAll(runtimeLibraryAtomicTargetIR, ") {", ") noinline {") + `
define void @cas64Store(ptr %p, i64 %old, i64 %new) noinline {
  %pair = call { i64, i1 } @cas64(ptr %p, i64 %old, i64 %new)
  ret void
}
`
	const consumer = `
#include <stdint.h>
uint32_t add32(uint32_t *, uint32_t);
uint64_t add64(uint64_t *, uint64_t);
void cas64Store(uint64_t *, uint64_t, uint64_t);
int main(void) {
  uint32_t a = 7;
  uint64_t b = 13;
  if (add32(&a, 5) != 7 || a != 12) return 1;
  if (add64(&b, 5) != 13 || b != 18) return 2;
  cas64Store(&b, 18, 42);
  if (b != 42) return 3;
  cas64Store(&b, 18, 99);
  return b != 42;
}
`
	for _, preserve := range []bool{false, true} {
		name := "ambient_only"
		if preserve {
			name = "serialized_attributes"
		}
		t.Run(name, func(t *testing.T) {
			prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0,lse"})
			defer prog.Dispose()
			mod := parseRuntimeLibraryTargetTestIR(t, prog, source)
			defer mod.Dispose()
			if preserve {
				if err := prepareRuntimeLibraryTargetAttributes(mod, prog.TargetSpec()); err != nil {
					t.Fatal(err)
				}
			}
			buffer := llvm.WriteFullLTOBitcodeToMemoryBuffer(mod, false)
			defer buffer.Dispose()
			dir := t.TempDir()
			provider := filepath.Join(dir, "runtime.o")
			if err := os.WriteFile(provider, buffer.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			main := filepath.Join(dir, "main.c")
			if err := os.WriteFile(main, []byte(consumer), 0600); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(dir, "probe")
			run(t, clang, "-target", "arm64-apple-macosx", "--sysroot="+sdk, "-mcpu=generic", "-fuse-ld=lld", "-flto=full", "-Wl,--lto-O2", main, provider, "-o", image)
			run(t, image)
			assembly := strings.ToLower(run(t, objdump, "--mattr=+lse", "--no-show-raw-insn", "--disassemble", image))
			for _, instruction := range []string{"ldaddal", "casal"} {
				if strings.Contains(assembly, instruction) != preserve {
					t.Fatalf("linked %s does not reflect serialized LSE=%v:\n%s", instruction, preserve, assembly)
				}
			}
			for _, instruction := range []string{"ldaxr", "stlxr"} {
				if strings.Contains(assembly, instruction) == preserve {
					t.Fatalf("linked %s does not reflect serialized LSE=%v:\n%s", instruction, preserve, assembly)
				}
			}
		})
	}
}

// Serializing a TargetMachine's module alone does not preserve its options.
// Reload through an explicitly featureless backend, as a linker may do, to
// distinguish retained function attributes from ambient frontend settings.
func TestRuntimeLibraryTargetAttributesSurviveFullBitcode(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, preserve := range []bool{false, true} {
		name := "ambient_only"
		if preserve {
			name = "serialized_attributes"
		}
		t.Run(name, func(t *testing.T) {
			producer := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0,lse"})
			defer producer.Dispose()
			mod := parseRuntimeLibraryTargetTestIR(t, producer)
			defer mod.Dispose()
			if preserve {
				if err := prepareRuntimeLibraryTargetAttributes(mod, producer.TargetSpec()); err != nil {
					t.Fatal(err)
				}
				before := mod.String()
				if err := prepareRuntimeLibraryTargetAttributes(mod, producer.TargetSpec()); err != nil || mod.String() != before {
					t.Fatalf("matching attributes are not idempotent: %v", err)
				}
				if !mod.NamedFunction("foreign").GetStringAttributeAtIndex(-1, "target-features").IsNil() {
					t.Fatal("foreign declaration acquired producer attributes")
				}
			}
			buffer := llvm.WriteFullLTOBitcodeToMemoryBuffer(mod, false)
			defer buffer.Dispose()
			file := filepath.Join(t.TempDir(), "runtime.o")
			if err := os.WriteFile(file, buffer.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			consumer := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", Resolved: &llssa.TargetSpec{
				Triple: producer.TargetSpec().Triple, CPU: "generic", Features: "+neon",
			}})
			defer consumer.Dispose()
			loaded, err := consumer.ParseBitcodeFile(file)
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Dispose()
			for _, name := range []string{"add32", "add64", "cas64"} {
				attribute := loaded.NamedFunction(name).GetStringAttributeAtIndex(-1, "target-features")
				if preserve && (attribute.IsNil() || attribute.GetStringValue() != producer.TargetSpec().Features) {
					t.Fatalf("%s lost its serialized features", name)
				}
				if !preserve && !attribute.IsNil() {
					t.Fatalf("%s unexpectedly inherited producer options", name)
				}
			}
			output, err := consumer.TargetMachine().EmitToMemoryBuffer(loaded, llvm.AssemblyFile)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Dispose()
			assembly := strings.ToLower(string(output.Bytes()))
			for _, instruction := range []string{"ldaddal", "casal"} {
				if strings.Contains(assembly, instruction) != preserve {
					t.Fatalf("%s does not reflect serialized LSE=%v:\n%s", instruction, preserve, assembly)
				}
			}
			for _, instruction := range []string{"ldaxr", "stlxr"} {
				if strings.Contains(assembly, instruction) == preserve {
					t.Fatalf("%s does not reflect serialized LSE=%v:\n%s", instruction, preserve, assembly)
				}
			}
		})
	}
}

func TestRuntimeLibraryTargetAttributesRejectConflictWithoutMutation(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, key := range []string{"target-cpu", "target-features"} {
		t.Run(key, func(t *testing.T) {
			prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0,lse"})
			defer prog.Dispose()
			mod := parseRuntimeLibraryTargetTestIR(t, prog)
			defer mod.Dispose()
			mod.NamedFunction("cas64").AddFunctionAttr(mod.Context().CreateStringAttribute(key, "conflicting-existing-contract"))
			before := mod.String()
			if err := prepareRuntimeLibraryTargetAttributes(mod, prog.TargetSpec()); err == nil {
				t.Fatal("conflicting producer attribute accepted")
			}
			if mod.String() != before {
				t.Fatal("conflict partly changed earlier definitions")
			}
		})
	}
}

func TestRuntimeLibraryTargetAttributesRejectMismatchedModule(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0,lse"})
	defer prog.Dispose()
	mod := parseRuntimeLibraryTargetTestIR(t, prog)
	defer mod.Dispose()
	target := prog.TargetSpec()
	target.Triple = "aarch64-unknown-linux"
	before := mod.String()
	if err := prepareRuntimeLibraryTargetAttributes(mod, target); err == nil || mod.String() != before {
		t.Fatalf("mismatched module was accepted or mutated: %v", err)
	}
}

func TestRuntimeLibraryTargetAttributesLeaveIncompleteProfilesUntouched(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, missing := range []string{"cpu", "features", "both"} {
		t.Run(missing, func(t *testing.T) {
			prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0,lse"})
			defer prog.Dispose()
			mod := parseRuntimeLibraryTargetTestIR(t, prog)
			defer mod.Dispose()
			// Preserve a producer's own attribute when an optional snapshot does not
			// give this pipeline a complete configuration to attach to definitions.
			mod.NamedFunction("cas64").AddFunctionAttr(mod.Context().CreateStringAttribute("target-cpu", "apple-m1"))
			target := prog.TargetSpec()
			if missing == "cpu" || missing == "both" {
				target.CPU = ""
			}
			if missing == "features" || missing == "both" {
				target.Features = ""
			}
			before := mod.String()
			if err := prepareRuntimeLibraryTargetAttributes(mod, target); err != nil || mod.String() != before {
				t.Fatalf("incomplete explicit profile was rejected or mutated: %v", err)
			}
		})
	}
}
