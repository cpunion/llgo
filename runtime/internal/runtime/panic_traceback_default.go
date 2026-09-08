//go:build !wasm

package runtime

type wasmPanicTrace struct{}

func snapshotWasmPanicFrames() bool {
	return false
}

func printWasmPanicTraceback(_ int) bool {
	return false
}

func panicShadowStackDepth(_ *callerLocationStore) int {
	return -1
}

func panicShadowStackFrame(_ *callerLocationStore, _ int, _ uintptr) (CallerFrame, bool) {
	return CallerFrame{}, false
}
