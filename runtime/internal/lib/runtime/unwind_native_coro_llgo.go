//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !wasm && !tinygo.wasm && !coro_runtime_adapter_test

package runtime

import (
	rtdebug "github.com/xgo-dev/llgo/runtime/internal/runtime"
	_ "unsafe"
)

// cCallerFrameMark returns the caller's native frame as an opaque scalar. The
// mark is never reconstructed as a Go pointer.
//
//go:linkname cCallerFrameMark C.llgo_caller_frame_mark
func cCallerFrameMark() uintptr

// cFrameChainContains performs the complete native-frame liveness walk on the
// current stack and exposes only the boolean result to Go.
//
//go:linkname cFrameChainContains C.llgo_frame_chain_contains
func cFrameChainContains(mark uintptr) int32

// recoverMark records an opaque point inside the recovering deferred frame's
// live native extent. The complete liveness walk remains in C so no stack
// address can cross a stackless-coroutine suspension point.
func recoverMark() {
	mark := cCallerFrameMark()
	if mark == 0 {
		return
	}
	rtdebug.MarkPanicRecoverFPs(mark, 0)
}

func panicSplicePCs() []uintptr {
	pcs := rtdebug.PanicPCs()
	if len(pcs) == 0 {
		return nil
	}
	if rtdebug.PanicActive() || rtdebug.CoroPanicRecoverActive() {
		return pcs
	}
	mark, _ := rtdebug.PanicRecoverFPs()
	if mark != 0 && cFrameChainContains(mark) != 0 {
		return pcs
	}
	return nil
}

// fpCallers asks the core runtime to copy the complete native chain while the
// synchronous C leaf still owns it. Only return PCs cross back into Go.
//
//go:noinline
func fpCallers(skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	initRuntimeFuncPCFrames()
	textLow, textHigh, ok := prebuiltTextBounds()
	if !ok {
		return 0
	}
	// PhysicalCallers adds one core-runtime wrapper frame.
	return rtdebug.PhysicalCallers(skip+1, pc, textLow, textHigh)
}
