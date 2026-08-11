//go:build !baremetal && !wasm && !tinygo.wasm

package runtime

import (
	rtdebug "github.com/goplus/llgo/runtime/internal/runtime"
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

func init() {
	rtdebug.PanicTraceback = panicTraceback
	rtdebug.PanicRecovered = clearFaultTraceback
	rtdebug.RecoverMark = recoverMark
}

// recoverMark records the recovering deferred frame (and one above, for
// wrapper-reached recover) so the panic snapshot stays spliceable while
// that frame is live. After siglongjmp the frame-pointer chain two levels
// up can point into a stale/reused stack region that is sometimes
// unmapped; probe each slot before dereferencing — an unguarded read here
// self-faults ~7% of the time, converting to a nil-deref panic that
// corrupts the value the recover was extracting (goroot reflectmake flake).
func recoverMark() {
	// Record this function's frame address: it sits below the recovering
	// deferred frame, and the liveness gate tests interval containment, so
	// the exact level does not matter.
	mark := cCallerFrameMark()
	if mark == 0 {
		return
	}
	rtdebug.MarkPanicRecoverFPs(mark, 0)
}

// panicSplicePCs returns the snapshot when it is observable: either the
// panic is still in flight, or the deferred frame that recovered it is
// still live on the physical chain (gc keeps panic frames on the stack
// exactly that long).
func panicSplicePCs() []uintptr {
	pcs := rtdebug.PanicPCs()
	if len(pcs) == 0 {
		return nil
	}
	if rtdebug.PanicActive() || rtdebug.CoroPanicRecoverActive() {
		return pcs
	}
	mark, _ := rtdebug.PanicRecoverFPs()
	if mark == 0 {
		return nil
	}
	// The C leaf performs interval containment over the live chain. Keeping
	// that walk outside Go prevents a native stack pointer from crossing a
	// coroutine safepoint.
	if cFrameChainContains(mark) != 0 {
		return pcs
	}
	return nil
}

// trimPlumbingPCs drops leading pcs attributed to the LLGo runtime core
// (panic machinery, the capture path) and cuts the tail at the first pc
// outside the program text — fault snapshots are captured without the
// text bound (see fpWalkFrom).
func trimPlumbingPCs(pcs []uintptr) []uintptr {
	initRuntimeFuncPCFrames()
	head := 0
	for head < len(pcs) {
		sym := frameSymbol(pcs[head] - 1)
		if sym.function != "" && (hasPrefix(sym.function, "github.com/goplus/llgo/runtime/internal/") ||
			sym.function == "runtime.capturePanicPCs" || sym.function == "runtime.onFault" ||
			sym.function == "runtime.fpWalkFrom") {
			head++
			continue
		}
		break
	}
	// Keep the innermost panic-machinery frame: gc's logical stack has
	// runtime.gopanic between the deferred function and the panic site,
	// and fixed Caller depths (issue5856's Caller(2)) count it. Fault
	// snapshots start at the fault pc and trim nothing — gc's walkers
	// skip runtime frames by name there, not by depth.
	if head > 0 {
		head--
	}
	pcs = pcs[head:]
	if rtdebug.PanicPCsAreFault() {
		// Fault snapshots come from a genuine interrupted context and the
		// chain-discipline guards already bounded the walk; keep unnamed
		// frames (linux dladdr cannot name non-dynamic C symbols — they
		// display as raw pcs, like gc does for unknown frames).
		return pcs
	}
	for i := 0; i < len(pcs); i++ {
		if prebuiltTextContains(pcs[i]) {
			continue
		}
		// Outside the Go text range: C frames in this binary (and its
		// libraries) still resolve to a symbol via dladdr — keep those;
		// cut at the first pc nothing can name (wild slots past the last
		// FP-disciplined frame).
		if frameSymbol(pcs[i]-1).function == "" {
			return pcs[:i]
		}
	}
	return pcs
}

// spliceCallers rebuilds the caller view a deferred function should see
// during (or right after recovering) a panic: its own live frames, then the
// panic-site chain from the snapshot. The junction is the first live frame
// whose function also appears in the snapshot — the defer owner; the
// snapshot side wins there because the live copy\'s pc points at the
// longjmp resume site, not at the call that panicked.
func spliceCallers(cur []uintptr) []uintptr {
	snap := panicSplicePCs()
	if len(snap) == 0 {
		return cur
	}
	snap = trimPlumbingPCs(snap)
	if len(snap) == 0 {
		return cur
	}
	// The junction is the first live frame whose function also appears in
	// the snapshot — the defer owner (or the panicking function itself when
	// defer and panic share a frame). Everything from there down is
	// replaced by the whole snapshot: it already contains the owner and its
	// callers, with the owner's pc on the panic path instead of the longjmp
	// resume site. Native stacks normally share an entry address. Stackless
	// frames use compiler-interned logical PCs, so their source identity may
	// have a different entry from the live CoroSplit resume function; in that
	// case the canonical function name is the stable cross-representation key.
	for i := 0; i < len(cur); i++ {
		live := frameSymbol(cur[i] - 1)
		if live.entry == 0 && live.function == "" {
			continue
		}
		for j := 0; j < len(snap); j++ {
			saved := frameSymbol(snap[j] - 1)
			sameEntry := live.entry != 0 && saved.entry == live.entry
			sameFunction := live.function != "" && saved.function == live.function
			sameSource := live.file != "" && saved.file == live.file &&
				live.line > 0 && saved.line == live.line
			if sameEntry || sameFunction || sameSource {
				out := make([]uintptr, 0, i+len(snap))
				out = append(out, cur[:i]...)
				out = append(out, snap...)
				return out
			}
		}
	}
	return cur
}

// copyPanicSplicedCallers applies skip only after constructing the complete
// logical panic-inclusive view. cur is a native FP walk for legacy execution
// and the compiler shadow stack for a stackless recovered coroutine.
func copyPanicSplicedCallers(cur []uintptr, skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	view := spliceCallers(cur)
	if skip < 0 {
		skip = 0
	}
	if skip >= len(view) {
		return 0
	}
	return copy(pc, view[skip:])
}

// callersWithPanicSplice is the legacy/native Callers overlay. The raw FP
// walk runs unskipped so splicing sees the junction frame, then the requested
// skip applies to the rebuilt view. Stackless callers supply their shadow
// stack directly from extern.go because entering this helper before capture
// would add the helper itself to that logical stack.
//
//go:noinline
func callersWithPanicSplice(skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	if len(rtdebug.PanicPCs()) == 0 {
		// One frame deeper than the extern.go call sites used to be.
		return fpCallers(skip+1, pc)
	}
	var raw [128]uintptr
	n := fpCallers(1, raw[:])
	if n <= 0 {
		return 0
	}
	return copyPanicSplicedCallers(raw[:n], skip, pc)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// panicTraceback prints a Go-style stack trace for an unrecovered panic:
// one "function(...)" line plus an indented file:line per physical frame,
// matching the shape of runtime.Stack and gc's panic output. Reports false
// (caller falls back to the clite dladdr dump) when the FP walk or the
// tables are unavailable.
func panicTraceback(skip int) bool {
	// Hardware-fault panics carry a fault-site pc snapshot; print that
	// chain (fault pc through the Go callers) instead of walking the
	// live stack, whose walk would start inside the fault plumbing.
	if faultTraceback(skip) {
		return true
	}
	if faultTracebackActive() {
		// The sidecar was not already available in signal context. Preserve
		// the async-signal failure policy through the resulting fatal panic:
		// use the clite/raw-PC fallback and never initiate filesystem I/O.
		return false
	}
	// Normal panic traceback is an I/O-safe first-use point. Hardware fault
	// traceback above deliberately never initiates sidecar loading.
	ensureRuntimePCLN()
	if !fpUnwindAvailable() {
		return false
	}
	var pcs [64]uintptr
	n := fpCallers(skip, pcs[:])
	if n <= 0 {
		return false
	}
	// A stored panic snapshot (Go panic or hardware fault, including
	// faults inside C code) carries the frames the longjmp unwinding
	// already removed; splice them in like Callers does.
	view := spliceCallers(pcs[:n])
	print("goroutine 1 [running]:\n")
	frames := CallersFrames(view)
	skippingPlumbing := true
	for {
		frame, more := frames.Next()
		name := frame.Function
		if name == "" {
			name = unknownFunctionName(frame.PC)
		}
		// The frames between the hook and the panic site are runtime
		// plumbing (Rethrow, Panic, ...); their depth varies by panic
		// path, so filter by package rather than a fixed skip.
		if skippingPlumbing {
			if hasPrefix(name, "github.com/goplus/llgo/runtime/internal/") {
				if more {
					continue
				}
				break
			}
			skippingPlumbing = false
		}
		print(name, "(...)\n\t")
		if frame.File == "" {
			print("???")
		} else {
			print(frame.File)
		}
		print(":", frame.Line)
		// gc appends the frame pc's offset from the function entry; the
		// value is codegen-specific, only the format matches.
		if frame.Entry != 0 && frame.PC >= frame.Entry {
			print(" +0x", string(appendHexUint(nil, uintptr(frame.PC-frame.Entry))))
		}
		print("\n")
		if !more {
			break
		}
	}
	return true
}

// maxFPStride is shared with the signal-fault snapshot walker. A decoded parent
// further away than any plausible native frame is a corrupt chain.
const maxFPStride = 1 << 20

// fpCallers walks the frame-pointer chain and fills pc with return
// addresses, Go-style: pc[0] is the return address in the frame `skip`
// levels above the caller of fpCallers. Every LLGo-compiled function keeps
// x29/rbp chained ("frame-pointer"="non-leaf" is set on all Go functions),
// so unlike the shadow stack this sees every physical frame; the walk stops
// at the first frame that breaks the chain discipline (e.g. foreign C code
// compiled without frame pointers).
//
// The clite walker (runtime/internal/clite/debug/_wrap/debug.c
// llgo_stacktrace) implements the same chain discipline and guards for the
// pre-table paths (unrecovered-panic dump, last-resort Callers fallback);
// keep the two in sync when changing the walk rules.
//
//go:noinline
func fpCallers(skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	// The walk bound needs the frame table's text range; make sure it is
	// built (no-op when the prebuilt table was adopted at startup).
	initRuntimeFuncPCFrames()
	textLow, textHigh, ok := prebuiltTextBounds()
	if !ok {
		return 0
	}
	// PhysicalCallers adds one core-runtime wrapper frame.
	return rtdebug.PhysicalCallers(skip+1, pc, textLow, textHigh)
}

// runtimeFPChain is emitted next to the funcinfo table (one per binary,
// internal/build emitFuncInfoTable) and records whether this binary's Go
// functions were compiled with the frame-pointer attribute
// (ssa.Program.NeedsFramePointer).
//
//go:linkname runtimeFPChain __llgo_fp_chain
var runtimeFPChain uint8

// fpUnwindAvailable reports whether the physical walk can be used for the
// public stack APIs: the compiler declared the FP chain intact for this
// binary, and the funcinfo tables are present (without them symbolization
// would fall back to dlsym anyway).
func fpUnwindAvailable() bool {
	return runtimePCLNReady() && runtimeFPChain != 0 && runtimeFuncInfoTable != nil && runtimeFuncInfoCount > 0
}
