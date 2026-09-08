//go:build !wasm

package runtime

type wasmPanicTrace struct{}

func snapshotWasmPanicFrames() bool {
	return false
}

func printWasmPanicTraceback(_ int) bool {
	return false
}
