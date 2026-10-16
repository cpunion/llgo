//go:build !llgo

package ssa_test

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/xgo-dev/llgo/internal/build"
	"github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/ssatest"
)

// Preserve the shared helper's target and optional Python configuration, but
// source runtime declarations from the same production loader as internal tests.
func newRuntimeTestProgram(t *testing.T, target *ssa.Target) ssa.Program {
	t.Helper()
	prog := ssatest.NewProgram(t, target)
	prog.SetRuntime(ssa.RuntimeTypesForTest(t))
	return prog
}

func init() {
	ssa.RuntimeTypeLoaderForTest = func() (*types.Package, error) {
		dir, err := os.MkdirTemp("", "llgo-ssa-runtime-types-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		source := filepath.Join(dir, "main.go")
		if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0600); err != nil {
			return nil, err
		}
		conf := build.NewDefaultConf(build.ModeGen)
		conf.ForceRebuild = true
		var runtimeTypes *types.Package
		conf.ModuleHook = func(pkg build.Package) {
			if pkg.Package != nil && pkg.PkgPath == ssa.PkgRuntime {
				runtimeTypes = pkg.Types
			}
		}
		packages, err := build.Do([]string{source}, conf)
		if err != nil {
			return nil, err
		}
		if len(packages) != 0 && packages[0].LPkg != nil {
			packages[0].LPkg.Prog.Dispose()
		}
		return runtimeTypes, nil
	}
}
