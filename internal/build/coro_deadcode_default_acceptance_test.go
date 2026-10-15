//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Supply a non-development compiler: the default under test must not rely on
// the development-only LLVM VFE pass or an explicit semantic-DCE opt-in. The
// harness invoking this opt-in gate owns the compiler/run RSS limit.
func TestCoroSemanticDeadcodeDefaultLinkAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_DCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_DCE_ACCEPTANCE=1 for real default method-slot DCE acceptance")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify a non-development compiler")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var nm string
	for _, name := range []string{"llvm-nm", "llvm-nm-22", "nm"} {
		if nm, err = exec.LookPath(name); err == nil {
			break
		}
	}
	if nm == "" {
		t.Fatal("method body retention gate requires llvm-nm or nm")
	}
	dropMethod := regexp.MustCompile(`(?m)^[^\n]*[tT] [^\n]*main[^\n]*T[^\n]*\.Drop(?:\$|\n|$)`)
	for _, test := range []struct {
		name    string
		fixture string
		flags   []string
		pruned  bool
	}{
		{"ordinary-staged", "direct_method", nil, false},
		{"O3-staged", "direct_method", []string{"-O3"}, false},
		{"full-LTO-default", "direct_method", []string{"-O3", "-lto=full"}, true},
		// A write-only interface sink can itself disappear during full LTO.
		// Reflection actually reads the descriptor, so this negative checks
		// that disabling semantic DCE keeps its unused callable method slot.
		{"full-LTO-explicit-false", "reflect_named_method", []string{"-O3", "-lto=full", "-deadcodedrop=false"}, false},
		{"interface-invoke", "interface_slot", []string{"-O3", "-lto=full"}, true},
		{"named-reflect-call", "reflect_named_method", []string{"-O3", "-lto=full"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "method-slots")
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
			defer cancel()
			args := append([]string{"build", "-p=1", "-o", image}, test.flags...)
			args = append(args, "./cl/_testdrop/"+test.fixture)
			command := exec.CommandContext(ctx, compiler, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("method-slot build: %v\n%s", err, output)
			}
			runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer runCancel()
			if output, err := exec.CommandContext(runCtx, image).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "42" {
				t.Fatalf("live interface/reflect method: %v\n%s", err, output)
			}
			symbols, err := exec.CommandContext(runCtx, nm, image).CombinedOutput()
			if err != nil {
				t.Fatalf("read method symbols: %v\n%s", err, symbols)
			}
			if retained := dropMethod.Match(symbols); retained == test.pruned {
				t.Fatalf("unused T.Drop retained = %v, want %v\n%s", retained, !test.pruned, symbols)
			}
		})
	}
}
