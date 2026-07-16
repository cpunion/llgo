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
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// coroPrepareExplicitPanicPrototype is intentionally not exported as a C ABI:
// compiler lowering does not yet publish SuspendPanic or prove the absence of
// cleanup/recover/Goexit/implicit-fault shapes. It demonstrates the future
// no-TLS hook boundary using only the physical G passed by generated code.
func coroPrepareExplicitPanicPrototype(
	g *coroG,
	handle unsafe.Pointer,
	header *coro.HeaderV1,
	typeWord, dataWord unsafe.Pointer,
) bool {
	return coro.PreparePanic(g, handle, header, typeWord, dataWord)
}

func coroLoadExplicitPanicPrototype(g *coroG) (coro.PanicRecordSnapshot, bool) {
	return coro.LoadPanicRecord(g)
}
