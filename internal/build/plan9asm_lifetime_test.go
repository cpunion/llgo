package build

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/cabi"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestPlan9AsmTemporaryObjectsFollowPackageLifetime(t *testing.T) {
	for _, laterFailure := range []bool{false, true} {
		name := "success"
		if laterFailure {
			name = "later source failure"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			t.Setenv("TMP", dir)
			t.Setenv("TEMP", dir)
			const path = "example.com/asm-lifetime"
			typesPkg := types.NewPackage(path, "fixture")
			typesPkg.Scope().Insert(types.NewFunc(token.NoPos, typesPkg, "First", types.NewSignatureType(nil, nil, nil, nil, nil, false)))
			typesPkg.Scope().Insert(types.NewFunc(token.NoPos, typesPkg, "Second", types.NewSignatureType(nil, nil, nil, nil, nil, false)))
			first, second := filepath.Join(dir, "first.s"), filepath.Join(dir, "second.s")
			secondSymbol := "Second"
			if laterFailure {
				secondSymbol = "MissingDeclaration"
			}
			for file, symbol := range map[string]string{first: "First", second: secondSymbol} {
				if err := os.WriteFile(file, []byte("TEXT ·"+symbol+"(SB),NOSPLIT,$0-0\nRET\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stable := filepath.Join(dir, "cache-owned.o")
			if err := os.WriteFile(stable, []byte("not owned by this compilation"), 0600); err != nil {
				t.Fatal(err)
			}
			pkg := &packages.Package{ID: path, PkgPath: path, Dir: dir, Types: typesPkg, TypesSizes: types.SizesFor("gc", runtime.GOARCH), OtherFiles: []string{first, second}}
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			compiled := &aPackage{Package: pkg, LPkg: prog.NewPackage("fixture", path), ObjFiles: []string{stable}}
			t.Cleanup(compiled.cleanupTemporaryObjFiles)
			ctx := &context{
				prog: prog, buildConf: &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
				commands:      commandEnv{dir: dir, environ: os.Environ()},
				plan9asmReady: true, plan9asmMode: plan9asmEnvAll,
				cTransformer: cabi.NewTransformer(prog, "", "", false),
			}
			objects, err := compilePkgSFiles(ctx, compiled, pkg, false)
			if laterFailure {
				if err == nil || !strings.Contains(err.Error(), "MissingDeclaration") || objects != nil {
					t.Fatalf("later translation failure = %v, objects %v", err, objects)
				}
			} else if err != nil || len(objects) != 2 {
				t.Fatalf("compile two assembly files = %v, objects %v", err, objects)
			}
			created, err := filepath.Glob(filepath.Join(dir, "plan9asm-*.o"))
			expectedObjects := 2
			if laterFailure {
				expectedObjects = 1
			}
			if err != nil || len(created) != expectedObjects {
				t.Fatalf("actual compiler-created objects = %v, %v", created, err)
			}
			for _, object := range created {
				if !slices.Contains(compiled.tempObjFiles, object) {
					t.Errorf("assembly object escaped package cleanup tracking: %s", object)
				}
			}
			compiled.cleanupTemporaryObjFiles()
			for _, object := range created {
				if _, err := os.Stat(object); !os.IsNotExist(err) {
					t.Errorf("temporary assembly object survives package cleanup: %s: %v", object, err)
				}
			}
			if data, err := os.ReadFile(stable); err != nil || string(data) != "not owned by this compilation" {
				t.Fatalf("cleanup modified a cache/user-owned archive member: %q, %v", data, err)
			}
		})
	}
}
