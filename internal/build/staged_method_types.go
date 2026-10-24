package build

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/xgo-dev/llgo/internal/dcepass"
	"github.com/xgo-dev/llgo/internal/lto"
	"github.com/xgo-dev/llvm"
)

const methodTypeFragmentVersion = 1

// This capability is independent of direct/LTO method DCE. In particular it
// must not disable package staging or the three private runtime optimizers.
func (c *Config) stagedMethodDCEEnabled() bool {
	return c != nil && c.Mode != ModeGen && !c.PackageCompileOnly &&
		c.BuildMode == BuildModeExe && c.ImportCfg == "" && !c.GenLL &&
		c.Target == "" && c.Goos == runtime.GOOS && c.Goarch == runtime.GOARCH &&
		(c.Goos == "darwin" || c.Goos == "linux") &&
		c.ltoMode() == lto.Off && !c.goGlobalDCEEnabled() &&
		!c.DeadcodeDropSet && !c.deadcodeDropEnabled()
}

type methodTypeFragment struct {
	Version     int
	Skipped     []string
	Bitcode     []byte
	Unavailable bool
}

func captureStagedMethodTypes(pkg *aPackage) error {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	mod := ctx.NewModule("method-types")
	defer mod.Dispose()
	src := pkg.LPkg.Module()
	mod.SetTarget(src.Target())
	mod.SetDataLayout(src.DataLayout())
	skipped := dcepass.CaptureMethodTypes(mod, src)
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("capture method types for %s: %w", pkg.PkgPath, err)
	}
	buf := llvm.WriteBitcodeToMemoryBuffer(mod)
	defer buf.Dispose()
	data, err := json.Marshal(methodTypeFragment{Version: methodTypeFragmentVersion, Skipped: skipped, Bitcode: buf.Bytes()})
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		// Oversized optional metadata must not reject a valid large program.
		data, err = json.Marshal(methodTypeFragment{Version: methodTypeFragmentVersion, Unavailable: true})
		if err != nil {
			return err
		}
	}
	pkg.MethodTypeFragment = data
	return nil
}

func decodeMethodTypeFragment(data []byte) (*methodTypeFragment, error) {
	var f methodTypeFragment
	if len(data) > 16<<20 || json.Unmarshal(data, &f) != nil || f.Version != methodTypeFragmentVersion ||
		(!f.Unavailable && !hasMethodTypeBitcodeHeader(f.Bitcode)) ||
		(f.Unavailable && (len(f.Bitcode) != 0 || len(f.Skipped) != 0)) {
		return nil, fmt.Errorf("invalid method type fragment")
	}
	return &f, nil
}

func hasMethodTypeBitcodeHeader(data []byte) bool {
	// LLVM writes a Darwin wrapper when the source module has a Mach-O target.
	return len(data) >= 4 && (string(data[:4]) == "BC\xc0\xde" || string(data[:4]) == "\xde\xc0\x17\x0b")
}

func readCachedMethodTypes(path, hash string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > 16<<20 {
		return nil, fmt.Errorf("oversized method type fragment")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if hash == "" || digestBytes(data) != hash {
		return nil, fmt.Errorf("method type fragment checksum mismatch")
	}
	_, err = decodeMethodTypeFragment(data)
	return data, err
}

func emitStagedMethodOverrides(dst llvm.Module, pkgs []Package, live map[string][]int, verbose bool) error {
	// A missing producer cannot prove any method dead. Check completeness
	// before changing the destination; malformed present artifacts are errors.
	for _, pkg := range pkgs {
		if len(pkg.MethodTypeFragment) == 0 {
			return nil
		}
		f, err := decodeMethodTypeFragment(pkg.MethodTypeFragment)
		if err != nil {
			return fmt.Errorf("method types for %s: %w", pkg.PkgPath, err)
		}
		if f.Unavailable {
			return nil
		}
	}
	seen := make(map[string]bool)
	for _, pkg := range pkgs {
		f, err := decodeMethodTypeFragment(pkg.MethodTypeFragment)
		if err != nil {
			return fmt.Errorf("method types for %s: %w", pkg.PkgPath, err)
		}
		for _, name := range f.Skipped {
			seen[name] = true
		}
		if err := emitMethodTypeFragment(dst, f.Bitcode, live, seen, verbose); err != nil {
			return fmt.Errorf("method types for %s: %w", pkg.PkgPath, err)
		}
	}
	return nil
}

func emitMethodTypeFragment(dst llvm.Module, bitcode []byte, live map[string][]int, seen map[string]bool, verbose bool) error {
	file, err := os.CreateTemp("", "llgo-method-types-*.bc")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(bitcode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	mod, err := ctx.ParseBitcodeFile(file.Name())
	if err != nil {
		return err
	}
	defer mod.Dispose()
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		return err
	}
	dcepass.EmitMethodTypeFragment(dst, mod, live, seen, verbose)
	return nil
}
