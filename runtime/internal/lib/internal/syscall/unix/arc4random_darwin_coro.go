//go:build darwin

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

package unix

import _ "unsafe"

// ARC4Random passes this fixed FuncPCABI0 target through syscall's private
// three-word carrier. Publishing its physical identity before conversion to
// uintptr lets the compiler derive the worker ABI and caller coloring from the
// exact sink without a source-level coroutine directive.
//
//go:linkname libc_arc4random_buf_trampoline C.arc4random_buf
func libc_arc4random_buf_trampoline()
