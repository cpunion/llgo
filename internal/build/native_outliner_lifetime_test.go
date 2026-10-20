//go:build !llgo && cgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// A module MachineOutliner separates selection from printing/freeing, forcing
// every machine function to stay resident. Check the actual legacy pipeline in
// isolated processes, so debug-pass options cannot affect unrelated compiles.
func TestLargeAArch64CodegenReleasesMachineFunctions(t *testing.T) {
	if !strings.HasPrefix(llvm.Version, "22.") {
		t.Skip("LLVM 22 machine-function lifetime contract")
	}
	const modeEnv = "LLGO_TEST_OUTLINER_LIFETIME_CHILD"
	if mode := os.Getenv(modeEnv); mode != "" {
		if mode != "enabled" && mode != "disabled" {
			t.Fatal("invalid child mode")
		}
		llssa.Initialize(llssa.InitAll)
		llvm.ParseCommandLineOptions([]string{"outliner-lifetime-test", "-debug-pass=Structure"}, "")
		prog := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64"})
		defer prog.Dispose()
		mod := prog.NewPackage("outliner", "outliner").Module()
		mod.SetTarget(prog.TargetSpec().Triple)
		mod.SetDataLayout(prog.DataLayout())
		ctx := mod.Context()
		fn := llvm.AddFunction(mod, "answer", llvm.FunctionType(ctx.Int32Type(), nil, false))
		b := ctx.NewBuilder()
		defer b.Dispose()
		b.SetInsertPointAtEnd(ctx.AddBasicBlock(fn, "entry"))
		b.CreateRet(llvm.ConstInt(ctx.Int32Type(), 42, false))
		setNativeMachineOutliner(prog.TargetMachine(), mode == "enabled")
		if err := emitNativeObjectFile(prog.TargetMachine(), mod, filepath.Join(t.TempDir(), "answer.o")); err != nil {
			t.Fatal(err)
		}
		return
	}
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, image, "-test.run=^TestLargeAArch64CodegenReleasesMachineFunctions$", "-test.count=1")
			cmd.Env = append(os.Environ(), modeEnv+"="+mode)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child: %v\n%s", err, out)
			}
			group, selection, printer, freed := -1, -1, -1, -1
			outlined := false
			for i, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "FunctionPass Manager") {
					group = i
				}
				if strings.TrimSpace(line) == "Machine Outliner" {
					outlined = true
				}
				if strings.TrimSpace(line) == "AArch64 Instruction Selection" {
					selection = group
				}
				if strings.TrimSpace(line) == "AArch64 Assembly Printer" {
					printer = group
				}
				if strings.TrimSpace(line) == "Free MachineFunction" {
					freed = group
				}
			}
			if selection < 0 || printer < 0 || freed < 0 {
				t.Fatalf("missing machine lifetime boundary:\n%s", out)
			}
			if mode == "enabled" {
				if !outlined || selection == printer {
					t.Fatalf("control does not retain all machine functions:\n%s", out)
				}
			} else if outlined || selection != printer || printer != freed {
				t.Fatalf("machine functions cannot be printed and freed individually:\n%s", out)
			}
		})
	}
}
