//go:build wasip1 && wasm

package runtime

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

//go:wasmimport wasi_snapshot_preview1 args_sizes_get
func wasiArgsSizesGet(argc, argvBufLen *uint32) uint32

//go:wasmimport wasi_snapshot_preview1 args_get
func wasiArgsGet(argv, argvBuf *byte) uint32

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	var argc, argvBufLen uint32
	if wasiArgsSizesGet(&argc, &argvBufLen) != 0 || argc == 0 {
		return nil
	}
	argv := make([]*byte, int(argc))
	if argvBufLen == 0 {
		argvBufLen = 1
	}
	argvBuf := make([]byte, int(argvBufLen))
	if wasiArgsGet((*byte)(unsafe.Pointer(&argv[0])), &argvBuf[0]) != 0 {
		return nil
	}
	args := make([]string, 0, len(argv))
	for _, value := range argv {
		if value == nil {
			break
		}
		args = append(args, c.GoString((*c.Char)(unsafe.Pointer(value))))
	}
	KeepAlive(argvBuf)
	return args
}
