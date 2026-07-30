//go:build js && wasm

package runtime

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	argc := int(c.Argc)
	if argc <= 0 || c.Argv == nil {
		return nil
	}
	args := make([]string, 0, argc)
	for i := 0; i < argc; i++ {
		p := c.Index(c.Argv, i)
		if p == nil {
			break
		}
		args = append(args, c.GoString(p))
	}
	return args
}
