//go:build !wasip2 && !wasm_unknown

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

// The scheduler-stack abort path is already fail-stop and cannot suspend into
// the managed worker protocol. Keep its stdio leaves behind private,
// synchronous declarations so ordinary c.Fputs/c.Fputc calls retain the
// conservative default-worker policy everywhere else.
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
