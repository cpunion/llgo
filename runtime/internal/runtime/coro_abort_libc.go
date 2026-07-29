//go:build !wasip1 && !wasip2 && !wasm_unknown

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// These private declarations are used only after a fatal runtime invariant has
// committed to process termination. Their exact C implementations return on
// the calling thread, invoke no Go callback, and retain no arguments. The
// bottom contract prevents this terminal diagnostic from trying to park on an
// already-invalid scheduler; ordinary c.Fputs/c.Fputc declarations remain
// independent may-block calls despite sharing the physical C symbols.
//
//llgo:coro sync
//go:linkname coroTerminalFputs C.fputs
func coroTerminalFputs(s *c.Char, fp c.FilePtr) c.Int

//llgo:coro sync
//go:linkname coroTerminalFputc C.fputc
func coroTerminalFputc(ch c.Int, fp c.FilePtr) c.Int

func coroRuntimeAbort(message string) {
	// Scheduler/runtime ABI failures happen on the executor stack and cannot
	// enter the general formatting or panic machinery: either path may require a
	// managed coroutine continuation. Emit the already-owned message one byte at
	// a time so this terminal path remains bounded and allocation-free while
	// still identifying the exact failed invariant.
	coroTerminalFputs(c.Str("fatal error: "), c.Stderr)
	for index := 0; index < len(message); index++ {
		coroTerminalFputc(c.Int(message[index]), c.Stderr)
	}
	coroTerminalFputc(c.Int('\n'), c.Stderr)
	c.Exit(2)
	// C.exit is declared through a bodyless C ABI and Go SSA cannot infer its
	// noreturn attribute. Keep this source function structurally terminal even
	// if a malformed platform shim were ever to return.
	for {
	}
}
