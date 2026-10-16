package runtime

import (
	"bytes"
	"context"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFuncForPCCacheTargetPolicy(t *testing.T) {
	for _, profile := range []struct {
		name, goos, goarch string
		tags               []string
		sets               int
	}{
		{"native64", "linux", "amd64", nil, 1024},
		{"darwin", "darwin", "arm64", nil, 1024},
		{"native32", "linux", "arm", nil, 1024},
		{"windows", "windows", "amd64", nil, 1024},
		{"wasi", "wasip1", "wasm", nil, 1024},
		{"js", "js", "wasm", nil, 1024},
		{"wasm-unknown", "linux", "arm", []string{"tinygo.wasm"}, 1024},
		{"cortex-m", "linux", "arm", []string{"baremetal"}, 32},
		{"riscv", "linux", "riscv64", []string{"baremetal"}, 32},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.BuildTags = profile.goos, profile.goarch, profile.tags
			selected := 0
			for _, name := range []string{"funcpc_cache_default_llgo.go", "funcpc_cache_baremetal_llgo.go"} {
				match, err := ctx.MatchFile("internal/lib/runtime", name)
				if err != nil {
					t.Fatal(err)
				}
				if !match {
					continue
				}
				selected++
				file, err := parser.ParseFile(token.NewFileSet(), "internal/lib/runtime/"+name, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				value := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec)
				sets, err := strconv.Atoi(value.Values[0].(*ast.BasicLit).Value)
				if err != nil || sets != profile.sets || sets <= 0 || sets&(sets-1) != 0 {
					t.Fatalf("%s: invalid cache sets %d (%v), want %d", name, sets, err, profile.sets)
				}
			}
			if selected != 1 {
				t.Fatalf("selected %d cache policies, want one", selected)
			}
		})
	}
}

// Execute the actual production lookup/replacement declarations. Only the
// symbol-table resolver is a deterministic fixture; no cache algorithm is
// copied into this test, and newly introduced dependencies fail compilation.
func TestFuncForPCCacheProductionCollisionAndRace(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/lib/runtime/pprof_runtime_stub_llgo.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{
		"funcForPCCacheWays": true, "funcForPCCache": true,
		"funcForPCCacheNext": true, "funcForPCLast": true,
		"cachedFuncForPC": true, "FuncForPC": true,
		"cacheFuncForPC": true, "funcForPCCacheIndex": true,
	}
	var declarations bytes.Buffer
	for _, decl := range file.Decls {
		var name string
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name = d.Name.Name
		case *ast.GenDecl:
			if len(d.Specs) == 1 {
				if value, ok := d.Specs[0].(*ast.ValueSpec); ok && len(value.Names) == 1 {
					name = value.Names[0].Name
				}
			}
		}
		if !names[name] {
			continue
		}
		delete(names, name)
		if err := format.Node(&declarations, fset, decl); err != nil {
			t.Fatal(err)
		}
		declarations.WriteString("\n")
	}
	if len(names) != 0 {
		t.Fatalf("missing production cache declarations: %v", names)
	}
	fixture, err := os.ReadFile("testdata/funcpc_cache/cache_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"default", "baremetal"} {
		t.Run(policy, func(t *testing.T) {
			dir := t.TempDir()
			policySource, err := os.ReadFile("internal/lib/runtime/funcpc_cache_" + policy + "_llgo.go")
			if err != nil {
				t.Fatal(err)
			}
			source := "package runtime\nimport (\"unsafe\"; latomic \"sync/atomic\")\n" + declarations.String()
			for name, data := range map[string][]byte{
				"cache.go": []byte(source), "policy.go": policySource, "cache_test.go": fixture,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runStage := func(stage string, timeout time.Duration, command string, args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				started := time.Now()
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GOWORK=off")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("production cache %s %s after %s (context=%v): %v\n%s", policy, stage, time.Since(started), ctx.Err(), err, output)
				}
				t.Logf("production cache %s %s: %s", policy, stage, time.Since(started))
			}
			// A cold race-toolchain build is not evidence that the cache lookup
			// hangs. Separate it from execution and give the actual concurrent
			// production algorithm a tighter bound than the former shared minute.
			image := filepath.Join(dir, "cache.test.exe")
			runStage("race build", 3*time.Minute, "go", "test", "-c", "-race", "-o", image, "cache.go", "policy.go", "cache_test.go")
			runStage("race execution", 30*time.Second, image, "-test.v", "-test.count=1", "-test.timeout=20s")
		})
	}
}
