// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	clitedebug "github.com/goplus/llgo/runtime/internal/clite/debug"
	rtdebug "github.com/goplus/llgo/runtime/internal/runtime"
)

// callerLocation substitutes gc's placeholders for missing position info:
// "???" for an unknown file and line 1 for an unknown line.
func callerLocation(file string, line int) (string, int) {
	if file == "" {
		file = "???"
	}
	if line == 0 {
		line = 1
	}
	return file, line
}

//go:noinline
func Caller(skip int) (pc uintptr, file string, line int, ok bool) {
	ensureRuntimePCLN()
	// A visible panic snapshot overlays the ordinary stack. It must be spliced
	// before consulting the compiler shadow stack; the latter is intentionally
	// non-empty for stackless coroutines but cannot contain frames already
	// removed by panic propagation or a C-worker fault landing.
	if len(panicSplicePCs()) != 0 {
		var pcs [1]uintptr
		count := 0
		if rtdebug.CoroPanicRecoverActive() {
			var current [128]uintptr
			if n := rtdebug.Callers(0, current[:]); n > 0 {
				count = copyPanicSplicedCallers(current[:n], skip+1, pcs[:])
			}
		} else if fpUnwindAvailable() {
			count = callersWithPanicSplice(skip+1, pcs[:])
		}
		if count >= 1 {
			pc = pcs[0] - 1
			sym := frameSymbol(pc)
			file, line = callerLocation(sym.file, sym.line)
			return pc, file, line, true
		}
	}
	if frame, ok := rtdebug.Caller(skip); ok {
		file, line = callerLocation(frame.File, frame.Line)
		return frame.PC, file, line, true
	}
	if fpUnwindAvailable() {
		var pcs [1]uintptr
		if callersWithPanicSplice(skip+1, pcs[:]) >= 1 {
			// Caller returns the call-instruction PC (matching Go), whereas
			// Callers returns return PCs. Keeping the adjusted value matters
			// when the return address equals the next function's entry.
			pc = pcs[0] - 1
			sym := frameSymbol(pc)
			file, line = callerLocation(sym.file, sym.line)
			return pc, file, line, true
		}
	}
	var pcs [1]uintptr
	if Callers(skip+2, pcs[:]) < 1 {
		return 0, "", 0, false
	}
	sym := frameSymbol(pcs[0])
	file, line = callerLocation(sym.file, sym.line)
	return pcs[0], file, line, true
}

//go:noinline
func Callers(skip int, pc []uintptr) int {
	ensureRuntimePCLN()
	if len(panicSplicePCs()) != 0 {
		n := 0
		if rtdebug.CoroPanicRecoverActive() {
			var current [128]uintptr
			if currentN := rtdebug.Callers(0, current[:]); currentN > 0 {
				n = copyPanicSplicedCallers(current[:currentN], skip, pc)
			}
		} else if fpUnwindAvailable() {
			n = callersWithPanicSplice(skip, pc)
		}
		if n > 0 {
			return n
		}
	}
	if n := rtdebug.Callers(skip, pc); n > 0 {
		return n
	}
	if fpUnwindAvailable() {
		if n := callersWithPanicSplice(skip, pc); n > 0 {
			return n
		}
	}
	return callers(skip+1, pc)
}

func callers(skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	n := 0
	for _, fr := range clitedebug.StackFrames(skip) {
		if n >= len(pc) {
			break
		}
		pc[n] = fr.PC
		recordFrameSymbol(fr.PC, fr.Offset, fr.Name)
		rtdebug.BindCallerLocation(fr.PC, fr.Name)
		n++
	}
	return n
}
