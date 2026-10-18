package importer

import (
	"go/token"
	"go/types"
)

// The companion Go toolchain supplies the gc export data consumed by LLGo's
// frontend. This reader does not consume LLGo's native coroutine archives.
func Default() types.Importer {
	return ForCompiler(token.NewFileSet(), "gc", nil)
}
