package build

import (
	"strings"

	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llvm"
)

// Emscripten can select native Wasm SjLj through SUPPORT_LONGJMP=wasm. LLVM
// requires the feature on each caller's IR function, even when emcc receives
// -fwasm-exceptions. Declaring the capability does not select the EH mode:
// Emscripten's default JS SjLj and explicit native SjLj keep their own lowering.
func applyEmscriptenEHFeature(ctx *context, mod llvm.Module) {
	profile := ctx.crossCompile.WasmProfile
	if profile != crosscompile.WasmProfileJ32 && profile != crosscompile.WasmProfileJ64 {
		return
	}
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		features := []string{"+exception-handling"}
		for _, attr := range fn.GetFunctionAttributes() {
			if attr.IsString() && attr.GetStringKind() == "target-features" {
				for _, feature := range strings.Split(attr.GetStringValue(), ",") {
					if feature != "" && feature != "+exception-handling" && feature != "-exception-handling" {
						features = append(features, feature)
					}
				}
			}
		}
		fn.AddFunctionAttr(mod.Context().CreateStringAttribute("target-features", strings.Join(features, ",")))
	}
}
