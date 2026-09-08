//go:build wasm

package runtime

type wasmPanicTrace struct {
	panicFrames []CallerFrame
}

// The native hook walks frame pointers. WebAssembly instead records compiler
// caller frames, which must be captured before longjmp discards deferred-call
// frames. Keep this a direct panic-path call, not an init-time function pointer:
// small programs must be able to discard unused traceback code and metadata.
func snapshotWasmPanicFrames() bool {
	store := callerLocationStoreCurrent
	if store != nil {
		// Printing a panic does not need stable synthetic PCs or the intern
		// table used by runtime.Callers. Copy the already-resolved frames;
		// deferred calls can then mutate the live stack without losing the
		// panic site. Preserve the existing 64-frame snapshot bound.
		start := len(store.stack) - 64
		if start < 0 {
			start = 0
		}
		store.panicFrames = append(store.panicFrames[:0], store.stack[start:]...)
	}
	return true
}

func printWasmPanicTraceback(_ int) bool {
	store := callerLocationStoreCurrent
	if store == nil {
		return false
	}
	printed := false
	for i := len(store.panicFrames) - 1; i >= 0; i-- {
		frame := &store.panicFrames[i]
		if frame.Function == "" {
			continue
		}
		if !printed {
			print("goroutine ", getg().goid, " [running]:\n")
			printed = true
		}
		print(frame.Function, "(...)\n\t")
		if frame.File == "" {
			print("???")
		} else {
			print(frame.File)
		}
		print(":", frame.Line, "\n")
	}
	return printed
}
