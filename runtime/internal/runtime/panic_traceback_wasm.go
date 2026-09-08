//go:build wasm

package runtime

type wasmPanicTrace struct {
	panicFrames     []CallerFrame
	panicFrameStart int
	panicStackDepth int
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
		store.panicFrameStart = start
		store.panicStackDepth = len(store.stack)
	}
	return true
}

// panicShadowStackDepth reports the logical shadow-stack depth while a panic
// is running deferred functions. gc exposes a runtime.gopanic frame between
// those live deferred calls and the saved panic-site stack. A recovering defer
// keeps that view until it returns, even though Recover has unlinked the panic.
func panicShadowStackDepth(store *callerLocationStore) int {
	if store == nil || len(store.panicFrames) == 0 ||
		store.panicStackDepth <= 0 || store.panicStackDepth > len(store.stack) {
		return -1
	}
	if !PanicActive() && len(store.stack) <= store.panicStackDepth {
		return -1
	}
	start := store.panicFrameStart
	if start < 0 || start+len(store.panicFrames) != store.panicStackDepth {
		return -1
	}
	for i := range store.panicFrames {
		if store.stack[start+i].Entry != store.panicFrames[i].Entry {
			return -1
		}
	}
	return len(store.stack) + 1
}

func panicShadowStackFrame(store *callerLocationStore, skip int, pcValue uintptr) (CallerFrame, bool) {
	depth := panicShadowStackDepth(store)
	if skip < 0 || skip >= depth {
		return CallerFrame{}, false
	}
	live := len(store.stack) - store.panicStackDepth
	if skip < live {
		return store.captureFrameAt(&store.stack[len(store.stack)-1-skip], pcValue), true
	}
	skip -= live
	if skip == 0 {
		return store.captureFrame(runtimeGopanicFrame, pcValue), true
	}
	skip--
	if skip < len(store.panicFrames) {
		return store.captureFrameAt(&store.panicFrames[len(store.panicFrames)-1-skip], pcValue), true
	}
	skip -= len(store.panicFrames)
	if skip < store.panicFrameStart {
		return store.captureFrameAt(&store.stack[store.panicFrameStart-1-skip], pcValue), true
	}
	return CallerFrame{}, false
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
