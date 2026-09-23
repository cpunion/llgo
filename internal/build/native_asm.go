package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xgo-dev/llgo/internal/packages"
	"github.com/xgo-dev/llgo/ssa/abi"
	llvm "github.com/xgo-dev/llvm"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func compileForeignARM64Asm(ctx *context, aPkg *aPackage, pkg *packages.Package, sfile string, src []byte) (string, bool, error) {
	if ctx.buildConf.Goos != "darwin" || ctx.buildConf.Goarch != "arm64" {
		return "", false, nil
	}
	_, decls := collectGoCgoPragmas(pkg.Syntax)
	if len(decls) == 0 {
		return "", false, nil
	}
	funcs := extplan9asm.ForeignARM64Functions(src)
	if len(funcs) == 0 {
		return "", false, nil
	}
	imports := make(map[string]string)
	for _, d := range decls {
		if prev, ok := imports[d.local]; ok && prev != d.alias {
			return "", true, fmt.Errorf("conflicting dynamic import %s", d.local)
		}
		imports[d.local] = d.alias
	}
	pkgPath := abi.PathOf(pkg.Types)
	assembly, data, err := extplan9asm.TranslateNativeARM64Source(src, imports, pkgPath)
	if err != nil {
		return "", true, err
	}
	dir, err := os.MkdirTemp("", "llgo-native-asm-")
	if err != nil {
		return "", true, err
	}
	defer os.RemoveAll(dir)
	gas := filepath.Join(dir, "native.s")
	if err = os.WriteFile(gas, []byte(assembly), 0600); err != nil {
		return "", true, err
	}
	output, err := os.CreateTemp("", "llgo-native-asm-*.o")
	if err != nil {
		return "", true, err
	}
	outpath := output.Name()
	output.Close()
	if err = ctx.irCompiler().Compile("-c", gas, "-o", outpath); err != nil {
		os.Remove(outpath)
		return "", true, err
	}
	// Reuse the normal DATA binding checks before changing the Go definitions.
	mod := aPkg.LPkg.Module().Context().NewModule("native-data")
	defer mod.Dispose()
	for _, d := range data {
		g := llvm.AddGlobal(mod, llvm.ArrayType(mod.Context().Int8Type(), int(d.Size)), d.Name)
		g.SetInitializer(llvm.ConstNull(g.GlobalValueType()))
	}
	if err = externalizePlan9DataGlobals(aPkg.LPkg.Module(), mod, ctx.prog.TargetData()); err != nil {
		os.Remove(outpath)
		return "", true, err
	}
	return outpath, true, nil
}
