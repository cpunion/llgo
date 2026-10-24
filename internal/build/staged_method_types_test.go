package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/internal/optlevel"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestStagedMethodTypesCacheIntegrityAndSourceRelease(t *testing.T) {
	program := llssa.NewProgram(nil)
	source := program.NewPackage("pkg", "pkg")
	addMethodTypeGlobal(source.Module(), "_llgo_pkg.T")
	pkg := &aPackage{Package: &packages.Package{PkgPath: "pkg"}, LPkg: source}
	if err := captureStagedMethodTypes(pkg); err != nil {
		t.Fatal(err)
	}
	pkg.LPkg = nil
	program.Dispose()
	fragment, err := decodeMethodTypeFragment(pkg.MethodTypeFragment)
	if err != nil || fragment.Unavailable {
		t.Fatalf("captured fragment: %v", err)
	}
	path := filepath.Join(t.TempDir(), "types")
	if err := os.WriteFile(path, pkg.MethodTypeFragment, 0600); err != nil {
		t.Fatal(err)
	}
	hash := digestBytes(pkg.MethodTypeFragment)
	if _, err := readCachedMethodTypes(path, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedMethodTypes(path, ""); err == nil {
		t.Fatal("accepted artifact without checksum")
	}
	corrupt := append(append([]byte(nil), pkg.MethodTypeFragment...), 'x')
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedMethodTypes(path, hash); err == nil {
		t.Fatal("accepted corrupt artifact")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedMethodTypes(path, hash); err == nil {
		t.Fatal("accepted missing artifact")
	}
	entryProgram := llssa.NewProgram(nil)
	defer entryProgram.Dispose()
	entry := entryProgram.NewPackage("entry", "entry")
	before := entry.String()
	if err := emitStagedMethodOverrides(entry.Module(), []Package{pkg, {Package: &packages.Package{PkgPath: "absent"}}}, nil, false); err != nil {
		t.Fatal(err)
	}
	if entry.String() != before {
		t.Fatal("incomplete fragments emitted overrides")
	}
	if err := emitStagedMethodOverrides(entry.Module(), []Package{pkg}, nil, false); err != nil {
		t.Fatal(err)
	}
	if entry.Module().NamedGlobal("_llgo_pkg.T").Initializer().IsNil() {
		t.Fatal("source-free fragment did not emit a definition")
	}
}

func TestStagedMethodTypesPreserveBackendAndExplicitOptOut(t *testing.T) {
	conf := &Config{Mode: ModeBuild, BuildMode: BuildModeExe, Goos: runtime.GOOS, Goarch: runtime.GOARCH, OptLevel: optlevel.Os}
	if !conf.stagedMethodDCEEnabled() || conf.deadcodeDropEnabled() || !conf.packageMetaEnabled() {
		t.Fatal("staged capability selected the direct backend")
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && !runtimeLibraryLTO(conf) {
		t.Fatal("disabled existing private runtime optimizer")
	}
	conf.DeadcodeDropSet = true
	if conf.stagedMethodDCEEnabled() {
		t.Fatal("ignored explicit opt out")
	}
	unavailable, _ := json.Marshal(methodTypeFragment{Version: methodTypeFragmentVersion, Unavailable: true})
	if _, err := decodeMethodTypeFragment(unavailable); err != nil {
		t.Fatal(err)
	}
}
