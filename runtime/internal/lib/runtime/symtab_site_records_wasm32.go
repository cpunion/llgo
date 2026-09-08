//go:build wasm && !llgo.wasm.emscripten.memory64

package runtime

// LLVM's wasm32 data layout aligns uint64 fields to eight bytes, while Go's
// wasm32 ABI aligns them to four. Keep the runtime declarations in step with
// the metadata records emitted as native LLVM structs.
type runtimeFuncInfoSymbolIndexRecord struct {
	symbolID  uint64
	funcIndex uint32
	_         uint32
}

type runtimeFuncInfoEntryRecord struct {
	pc uintptr
	_  uint32

	symbolID uint64
}

type runtimePCSiteRecord struct {
	pc uintptr
	_  uint32

	id uint64
}
