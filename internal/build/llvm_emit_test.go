package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llgo/internal/lto"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestUseInMemoryNativeCodegenConf(t *testing.T) {
	t.Run("native host", func(t *testing.T) {
		conf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH}
		if !useInMemoryNativeCodegenConf(conf) {
			t.Fatal("expected native host build to use in-memory native codegen")
		}
	})

	t.Run("embedded target", func(t *testing.T) {
		conf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, Target: "rp2040"}
		if useInMemoryNativeCodegenConf(conf) {
			t.Fatal("expected embedded target build to keep using clang")
		}
	})

	t.Run("full LTO", func(t *testing.T) {
		conf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, LTO: lto.Full}
		if !useInMemoryNativeCodegenConf(conf) {
			t.Fatal("expected native full LTO build to use in-memory bitcode emission")
		}
	})

	t.Run("cross compile host mismatch", func(t *testing.T) {
		goos := runtime.GOOS
		goarch := runtime.GOARCH
		if goos == "linux" {
			goos = "darwin"
		} else {
			goos = "linux"
		}
		if goarch == "amd64" {
			goarch = "arm64"
		} else {
			goarch = "amd64"
		}
		conf := &Config{Goos: goos, Goarch: goarch}
		if useInMemoryNativeCodegenConf(conf) {
			t.Fatal("expected host mismatch to keep using clang")
		}
	})

	t.Run("wasm", func(t *testing.T) {
		conf := &Config{Goos: "wasip1", Goarch: "wasm"}
		if useInMemoryNativeCodegenConf(conf) {
			t.Fatal("expected wasm target to keep using clang")
		}
	})
}

func TestThinLTOEmissionNamesAnonymousGlobals(t *testing.T) {
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("p", "example.com/p")
	byteType := pkg.Module().Context().Int8Type()
	anonymous := llvm.AddGlobal(pkg.Module(), byteType, "")
	anonymous.SetInitializer(llvm.ConstInt(byteType, 1, false))
	anonymous.SetLinkage(llvm.PrivateLinkage)
	anonymous.SetGlobalConstant(true)

	if global := pkg.Module().FirstGlobal(); global.IsNil() {
		t.Fatal("fixture has no global")
	} else if global.Name() != "" {
		t.Fatalf("fixture first global name = %q, want anonymous", global.Name())
	}
	ctx := &context{
		buildConf: &Config{
			Goos:   runtime.GOOS,
			Goarch: runtime.GOARCH,
			LTO:    lto.Thin,
		},
		prog: prog,
	}
	buffer, kind, err := emitObjectToMemoryBuffer(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Dispose()
	if len(buffer.Bytes()) == 0 {
		t.Fatal("ThinLTO emission returned an empty bitcode buffer")
	}
	if !strings.Contains(kind, "ThinLTO") {
		t.Fatalf("emission kind = %q, want ThinLTO", kind)
	}
	for global := pkg.Module().FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if global.Name() == "" {
			t.Fatalf("anonymous global remains after ThinLTO emission: %s", global.String())
		}
	}
}

func TestExportPackageObjectErrors(t *testing.T) {
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("p", "example.com/p")

	t.Run("clang", func(t *testing.T) {
		ctx := &context{
			buildConf: &Config{Target: "embedded"},
			crossCompile: crosscompile.Export{
				CC: filepath.Join(t.TempDir(), "missing-clang"),
			},
			commands: commandEnv{environ: os.Environ()},
		}
		path, member, err := exportPackageObject(ctx, pkg.Path(), "p.o", pkg)
		if path != "" {
			defer os.Remove(path)
		}
		if err == nil {
			member.buffer.Dispose()
			t.Fatal("exportPackageObject succeeded with a missing clang")
		}
		if !member.buffer.IsNil() {
			member.buffer.Dispose()
			t.Fatal("clang export returned an in-memory archive member")
		}
	})

	t.Run("IR dump", func(t *testing.T) {
		ctx := &context{buildConf: &Config{
			Goos:         runtime.GOOS,
			Goarch:       runtime.GOARCH,
			CheckLLFiles: true,
		}}
		exportFile := strings.Repeat("x", 300)
		path, member, err := exportPackageObject(ctx, pkg.Path(), exportFile, pkg)
		if path != "" {
			defer os.Remove(path)
		}
		if err == nil {
			member.buffer.Dispose()
			t.Fatal("exportPackageObject succeeded with an overlong IR dump prefix")
		}
		if !member.buffer.IsNil() {
			member.buffer.Dispose()
			t.Fatal("failed IR dump returned an in-memory archive member")
		}
	})
}
