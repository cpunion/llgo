package build

import (
	"encoding/json"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/frontcache"
	"github.com/xgo-dev/llgo/internal/packages"
	gopackages "golang.org/x/tools/go/packages"
)

func TestFrontendCacheRejectsPreviousABISpellingSchema(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		accept  bool
	}{
		{"previous", 3, false},
		{"current", frontendExportVersion, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &context{buildConf: &Config{}, cacheManager: &cacheManager{root: t.TempDir()}}
			pkg := &aPackage{Package: &packages.Package{ID: "example/dep", PkgPath: "example/dep", Name: "dep", Module: &gopackages.Module{}}}
			f := &frontendCache{ctx: ctx, keys: map[string]string{pkg.ID: "source"}, programInput: "program"}
			record := &frontendExport{Version: tc.version, Input: "source", Archive: "archive", Link: &frontendLinkExport{}, Functions: []frontendFunction{{ID: "F"}}}
			writeFrontendRecord(t, f.path("source"), record)
			if got := f.load(pkg); (got != nil) != tc.accept {
				t.Fatalf("package companion accepted = %v, want %v", got != nil, tc.accept)
			}

			loader := &frontendTypeLoader{ctx: ctx, keys: map[string]string{pkg.ID: "source"}, selected: make(map[string]bool)}
			typesRecord := &frontendTypeExport{Version: tc.version, Input: "source", Frontend: "source", Archive: "archive", Package: &frontcache.PackageExport{ID: pkg.ID, Path: pkg.PkgPath, Name: pkg.Name}}
			writeFrontendRecord(t, loader.path("source"), typesRecord)
			archive := ctx.ensureCacheManager().PackagePaths(ctx.targetTriple(), pkg.PkgPath, "archive").Archive
			if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(archive, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if got := loader.load(pkg.Package, token.NewFileSet()); got != tc.accept {
				t.Fatalf("type companion accepted = %v, want %v", got, tc.accept)
			}
			if !tc.accept && (loader.selected[pkg.ID] || pkg.Types != nil) {
				t.Fatal("rejected type companion replaced the source type-checking path")
			}

			program := &frontendProgramExport{Version: tc.version, Input: "program"}
			writeFrontendRecord(t, f.programPath(), program)
			if got := f.loadProgram(); (got != nil) != tc.accept {
				t.Fatalf("program companion accepted = %v, want %v", got != nil, tc.accept)
			}
		})
	}
}

func TestFrontendSourceClosureHandlesReplacementCycles(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "a.go")
	makeCache := func(source string) *frontendCache {
		a := &aPackage{Package: &packages.Package{ID: "a", PkgPath: "a", Name: "a", GoFiles: []string{filename}}}
		b := &aPackage{Package: &packages.Package{ID: "b", PkgPath: "b", Name: "b"}}
		a.Imports = map[string]*packages.Package{"b": b.Package}
		b.Imports = map[string]*packages.Package{"a": a.Package}
		ctx := &context{conf: &packages.Config{}, mode: ModeGen, buildConf: &Config{Overlay: map[string][]byte{filename: []byte(source)}}, llvmVersion: "test", pkgByID: map[string]*aPackage{"a": a, "b": b}}
		f := &frontendCache{ctx: ctx, keys: make(map[string]string), locals: make(map[string]string)}
		ctx.frontend = f
		return f
	}
	first := makeCache("package a")
	a, err := first.inputKey(first.ctx.pkgByID["a"])
	if err != nil {
		t.Fatal(err)
	}
	b, err := first.inputKey(first.ctx.pkgByID["b"])
	if err != nil {
		t.Fatal(err)
	}
	second := makeCache("package a")
	b2, _ := second.inputKey(second.ctx.pkgByID["b"])
	a2, _ := second.inputKey(second.ctx.pkgByID["a"])
	if a == "" || b == "" || a != a2 || b != b2 {
		t.Fatal("cycle identity depends on traversal order")
	}
	for _, id := range []string{"a", "b"} {
		if err := first.ctx.collectFingerprint(first.ctx.pkgByID[id]); err != nil {
			t.Fatal(err)
		}
	}
	if len(first.ctx.cacheDisabled) != 0 {
		t.Fatal("validated replacement cycle disabled archives")
	}
	changed := makeCache("package a;type T int")
	for _, id := range []string{"a", "b"} {
		key, err := changed.inputKey(changed.ctx.pkgByID[id])
		if err != nil || key == first.keys[id] {
			t.Fatalf("source overlay did not invalidate %s: %v", id, err)
		}
	}
}

func TestFrontendExportsMethodsCallerRecoverAndGenericBodies(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLGO_ROOT", root)
	t.Setenv(llgoBuildCache, "1")
	t.Setenv(llgoEscapeCache, "1")
	cache := t.TempDir()
	old := cacheRootFunc
	cacheRootFunc = func() string { return cache }
	t.Cleanup(func() { cacheRootFunc = old })
	fixture := filepath.Join(root, "internal/build/testdata/frontendcache")
	baseline := exec.Command("go", "run", ".")
	baseline.Dir = fixture
	if out, err := baseline.CombinedOutput(); err != nil || !strings.Contains(string(out), "frontend-ok") {
		t.Fatalf("Go baseline: %v\n%s", err, out)
	}
	for _, phase := range []string{"cold", "hot"} {
		cfg := NewDefaultConf(ModeBuild)
		cfg.BuildParallelism = 8
		cfg.OutFile = filepath.Join(t.TempDir(), "probe")
		if runtime.GOOS == "windows" {
			cfg.OutFile += ".exe"
		}
		cfg.BuildTrace = filepath.Join(t.TempDir(), "trace.json")
		if _, err := Build(Invocation{Args: []string{"."}, Dir: fixture, Config: cfg}); err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
		if out, err := exec.Command(cfg.OutFile).CombinedOutput(); err != nil || !strings.Contains(string(out), "frontend-ok") {
			t.Fatalf("%s runtime: %v\n%s", phase, err, out)
		}
		if phase == "cold" {
			continue
		}
		data, err := os.ReadFile(cfg.BuildTrace)
		if err != nil {
			t.Fatal(err)
		}
		var events []buildTraceEvent
		if err := json.Unmarshal(data, &events); err != nil {
			t.Fatal(err)
		}
		imports := 0
		inlineBuilt := false
		for _, e := range events {
			if e.Name == "certify frontend exports" {
				if n, ok := e.Args["type_exports"].(float64); ok {
					imports = int(n)
				}
			}
			if e.Args["stage"] == "ssa" {
				switch e.Args["package_id"] {
				case "example.com/frontprobe/dep":
					t.Fatal("warm build reconstructed opaque dependency SSA")
				case "example.com/frontprobe/inline":
					inlineBuilt = true
				}
			}
		}
		if imports == 0 || !inlineBuilt {
			t.Fatalf("type imports=%d inline source built=%v", imports, inlineBuilt)
		}
	}
}
