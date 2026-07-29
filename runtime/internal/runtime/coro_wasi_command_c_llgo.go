//go:build llgo && llgo_coro && wasip1 && (wasm || tinygo.wasm) && !baremetal

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

// The Preview 1 command reactor is a plain C/WASI boundary: it is entered only
// after a managed RunSlice has returned and calls poll_oneoff only while no
// scheduler activation exists on the machine stack. The Go compiler entry
// owns the calls to its two fixed symbols.
const LLGoFiles = "_wrap/coro_wasi_command.c"
