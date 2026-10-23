//go:build !llgo

package cabi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestSmallAggregateReturnPreservesLoadedSnapshot(t *testing.T) {
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	llvm.InitializeAllTargetInfos()
	for _, target := range []struct {
		goos, goarch, triple string
	}{
		{"darwin", "arm64", "arm64-apple-darwin"},
		{"linux", "amd64", "x86_64-unknown-linux-gnu"},
	} {
		t.Run(target.goarch, func(t *testing.T) {
			for _, optimize := range []bool{false, true} {
				ctx := llvm.NewContext()
				const input = `%Pair = type { i32, i32 }
@snapshot_source = external global %Pair
declare void @mutate_snapshot_source()
define %Pair @preserve_snapshot() {
entry:
  %snapshot = load %Pair, ptr @snapshot_source, align 4
  call void @mutate_snapshot_source()
  ret %Pair %snapshot
}
`
				path := filepath.Join(t.TempDir(), "snapshot.ll")
				if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
					t.Fatal(err)
				}
				buffer, err := llvm.NewMemoryBufferFromFile(path)
				if err != nil {
					t.Fatal(err)
				}
				module, err := ctx.ParseIR(buffer)
				if err != nil {
					t.Fatal(err)
				}
				prog := llssa.NewProgram(&llssa.Target{GOOS: target.goos, GOARCH: target.goarch})
				NewTransformer(prog, target.triple, "", ModeAllFunc, optimize).TransformModule("test", module)
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err, module.String())
				}
				options := llvm.NewPassBuilderOptions()
				err = module.RunPasses("function(sroa,instcombine)", prog.TargetMachine(), options)
				options.Dispose()
				if err != nil {
					t.Fatal(err)
				}
				fn := module.NamedFunction("preserve_snapshot")
				mutated, sourceReads := false, 0
				for inst := fn.EntryBasicBlock().FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
					text := inst.String()
					if !inst.IsACallInst().IsNil() && strings.Contains(text, "@mutate_snapshot_source(") {
						mutated = true
					}
					if !inst.IsALoadInst().IsNil() && strings.Contains(text, "@snapshot_source") {
						sourceReads++
						if mutated {
							t.Fatalf("returned aggregate reloads mutated source (optimize=%t):\n%s", optimize, fn.String())
						}
					}
				}
				if !mutated || sourceReads == 0 {
					t.Fatalf("snapshot control was not retained (optimize=%t):\n%s", optimize, fn.String())
				}
				prog.Dispose()
				module.Dispose()
				ctx.Dispose()
			}
		})
	}
}
