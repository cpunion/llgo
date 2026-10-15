//go:build !llgo

package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llgo/internal/packages"
)

func TestNativeObjectsIsolateConcurrentBuildsAndRecipes(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	nm := siblingTool(clang, "llvm-nm")
	if nm == "" {
		nm, err = exec.LookPath("llvm-nm")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	sharedExport := filepath.Join(dir, "go-export")
	if err := os.WriteFile(sharedExport, []byte("shared Go export data"), 0644); err != nil {
		t.Fatal(err)
	}
	sources := make([]string, 2)
	for i, subdir := range []string{"first", "second"} {
		sourceDir := filepath.Join(dir, subdir)
		if err := os.Mkdir(sourceDir, 0755); err != nil {
			t.Fatal(err)
		}
		sources[i] = filepath.Join(sourceDir, "leaf.c")
		if err := os.WriteFile(sources[i], []byte("int SYMBOL(void) { return 42; }\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	newContext := func() *context {
		ctx := &context{
			buildConf:    &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
			crossCompile: crosscompile.Export{CC: clang},
			commands:     commandEnv{dir: dir, environ: os.Environ()},
		}
		t.Cleanup(ctx.nativeObjectWorkspace().cleanup)
		return ctx
	}
	for _, sameBuild := range []bool{false, true} {
		name := "same_source_different_build_flags"
		if sameBuild {
			name = "same_build_same_basename_isolated_backends"
		}
		t.Run(name, func(t *testing.T) {
			owners := []*context{newContext(), newContext()}
			contexts := owners
			inputs := []string{sources[0], sources[0]}
			if sameBuild {
				owners[1] = owners[0]
				contexts = []*context{owners[0].newBackendTask(backendSession{}), owners[0].newBackendTask(backendSession{})}
				inputs[1] = sources[1]
			}
			objects := make([]string, 2)
			errs := make([]error, 2)
			symbols := []string{"native_recipe_first", "native_recipe_second"}
			var wg sync.WaitGroup
			for i := range contexts {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					errs[i] = runPackageJob(i, func(int) error {
						clFile(contexts[i], []string{"-DSYMBOL=" + symbols[i]}, inputs[i], sharedExport, "fixture", func(path string) { objects[i] = path }, false)
						return nil
					})
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("compile %d: %v", i, err)
				}
			}
			// Both compilations finish before either archive consumes its input.
			// Shared output paths therefore fail deterministically, not by timing.
			if objects[0] == objects[1] {
				t.Fatalf("independent native recipes share writable output %q", objects[0])
			}
			archives := make([]string, 2)
			for i, object := range objects {
				pkg := &aPackage{Package: &packages.Package{PkgPath: "fixture"}, ObjFiles: []string{object}}
				if err := normalizeToArchive(owners[i], pkg, false); err != nil {
					t.Fatal(err)
				}
				archives[i] = pkg.ArchiveFile
				t.Cleanup(func() { _ = os.Remove(pkg.ArchiveFile) })
			}
			for _, owner := range owners {
				owner.nativeObjectWorkspace().cleanup()
			}
			for i, object := range objects {
				if _, err := os.Stat(object); !os.IsNotExist(err) {
					t.Fatalf("native temporary object survived archive publication: %v", err)
				}
				output, err := exec.Command(nm, "--defined-only", archives[i]).CombinedOutput()
				if err != nil || !strings.Contains(string(output), symbols[i]) || strings.Contains(string(output), symbols[1-i]) {
					t.Fatalf("archive %d lost its exact native recipe: %v\n%s", i, err, output)
				}
			}
			if _, err := os.Stat(sharedExport + "leaf.c.o"); !os.IsNotExist(err) {
				t.Fatalf("native compiler wrote next to shared Go export data: %v", err)
			}
		})
	}
}

func TestNativeObjectWorkspaceFailureCleanup(t *testing.T) {
	ctx := &context{}
	workspace := ctx.nativeObjectWorkspace()
	path, err := workspace.create("leaf.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	workspace.cleanup()
	workspace.cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("failed-build workspace survived cleanup: %v", err)
	}
}
