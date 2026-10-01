package asmselect

import (
	"encoding/json"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestHeaderOverlayPreservesOrdinaryTestNamedAssembly(t *testing.T) {
	for _, target := range []struct {
		goos, goarch string
	}{
		{"linux", "386"},
		{"linux", "amd64"},
		{"linux", "arm"},
		{"linux", "arm64"},
		{"js", "wasm"},
	} {
		for _, qualified := range []bool{false, true} {
			name := "probe_test.s"
			if qualified {
				name = "probe_test_" + target.goarch + ".s"
			}
			t.Run(target.goos+"/"+target.goarch+"/"+name, func(t *testing.T) {
				dir, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				sources := map[string]string{
					"go.mod":         "module example.com/ordinary-assembly\n\ngo 1.27.0\n",
					"native.go":      "//go:build !llgo\n\npackage probe\nfunc Kernel()\n",
					"poison_test.go": "package probe\nthis is invalid Go test source\n",
					name: "//go:build !llgo\n\n#include \"textflag.h\"\n" +
						"TEXT ·Kernel(SB), NOSPLIT, $0-0\nRET\n",
				}
				for file, source := range sources {
					if err := os.WriteFile(filepath.Join(dir, file), []byte(source), 0600); err != nil {
						t.Fatal(err)
					}
				}

				// Go itself selects these assembly basenames in an ordinary
				// package and produces an object without loading poison_test.go.
				ordinaryAssemblyPackage(t, dir, target.goos, target.goarch, name, "")
				context := build.Default
				context.GOOS, context.GOARCH = target.goos, target.goarch
				context.BuildTags = nil
				overlay, err := HeaderOverlay(dir, context)
				if err != nil {
					t.Fatalf("Go-selected ordinary assembly %s was rejected: %v", name, err)
				}

				replace := make(map[string]string, len(overlay))
				for original, body := range overlay {
					replacement := filepath.Join(t.TempDir(), filepath.Base(original))
					if err := os.WriteFile(replacement, body, 0600); err != nil {
						t.Fatal(err)
					}
					replace[original] = replacement
				}
				data, err := json.Marshal(struct {
					Replace map[string]string
				}{replace})
				if err != nil {
					t.Fatal(err)
				}
				mapfile := filepath.Join(t.TempDir(), "overlay.json")
				if err := os.WriteFile(mapfile, data, 0600); err != nil {
					t.Fatal(err)
				}
				ordinaryAssemblyPackage(t, dir, target.goos, target.goarch, name, mapfile)

				for file, expected := range sources {
					actual, err := os.ReadFile(filepath.Join(dir, file))
					if err != nil || string(actual) != expected {
						t.Fatalf("original source changed: %s: %v", file, err)
					}
				}
			})
		}
	}
}

func ordinaryAssemblyPackage(t *testing.T, dir, goos, goarch, assembly, overlay string) {
	t.Helper()
	args := []string{"list", "-export", "-compiled", "-json", "-tags="}
	if overlay != "" {
		args = []string{"list", "-export", "-compiled", "-json", "-tags=llgo", "-overlay=" + overlay}
	}
	cmd := exec.Command("go", append(args, ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch,
		"CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Go %s/%s ordinary export failed: %v\n%s", goos, goarch, err, output)
	}
	var pkg struct {
		GoFiles, CompiledGoFiles, SFiles []string
		Export                           string
	}
	if err := json.Unmarshal(output, &pkg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pkg.GoFiles, []string{"native.go"}) ||
		!slices.Equal(pkg.SFiles, []string{assembly}) || len(pkg.CompiledGoFiles) != 1 {
		t.Fatalf("ordinary source selection changed: %+v", pkg)
	}
	if pkg.Export == "" {
		t.Fatal("actual Go export did not produce an object")
	}
	info, err := os.Stat(pkg.Export)
	if err != nil || info.Size() == 0 {
		t.Fatalf("actual Go object missing or empty: %q: %v", pkg.Export, err)
	}
}
