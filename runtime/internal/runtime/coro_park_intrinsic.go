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

	"github.com/goplus/llgo/runtime/internal/coro"
)

// coroPark is the compiler-owned source spelling for an exact current-frame
// park. It intentionally has no ordinary Go body: cl lowers a direct call in
// the caller's physical coroutine to publish/prepare/suspend/activate. Future
// channel, timer, syscall, and platform adapters may call this declaration
// while preserving their synchronous Go source signatures.
//
//go:linkname coroPark llgo.coroPark
func coroPark(token *coro.WaitToken, ticket coro.WaitTicket)
