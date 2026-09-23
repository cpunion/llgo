package build

import (
	"fmt"

	"github.com/xgo-dev/llgo/internal/packages"
	"github.com/xgo-dev/llgo/ssa/abi"
	llvm "github.com/xgo-dev/llvm"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func compileForeignNativeAsm(ctx *context, aPkg *aPackage, pkg *packages.Package, src []byte) (bool, error) {
	if !extplan9asm.SupportsNativeTarget(ctx.buildConf.Goos, ctx.buildConf.Goarch) {
		return false, nil
	}
	_, decls := collectGoCgoPragmas(pkg.Syntax)
	if len(decls) == 0 {
		return false, nil
	}
	funcs := extplan9asm.ForeignNativeFunctions(src, ctx.buildConf.Goarch)
	if len(funcs) == 0 {
		return false, nil
	}
	imports := make(map[string]string)
	for _, d := range decls {
		if prev, ok := imports[d.local]; ok && prev != d.alias {
			return true, fmt.Errorf("conflicting dynamic import %s", d.local)
		}
		imports[d.local] = d.alias
	}
	pkgPath := abi.PathOf(pkg.Types)

	goMod := aPkg.LPkg.Module()
	mod, err := extplan9asm.TranslateNativeModule(goMod.Context(), src, extplan9asm.NativeOptions{GOOS: ctx.buildConf.Goos, GOARCH: ctx.buildConf.Goarch, PackagePath: pkgPath, Imports: imports})
	if err != nil {
		return true, err
	}
	mod.SetTarget(ctx.prog.Target().Spec().Triple)
	mod.SetDataLayout(ctx.prog.DataLayout())
	if err = externalizePlan9DataGlobals(goMod, mod, ctx.prog.TargetData()); err != nil {
		mod.Dispose()
		return true, err
	}
	// LinkModules consumes mod. Native functions/data now follow the package's
	// normal optimization, bitcode/LTO and object-emission pipeline.
	return true, llvm.LinkModules(goMod, mod)
}
