//go:build !wasm

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

package chacha8rand

//llgo:skip block

// LLGo does not lower every optimized vector assembly implementation of block.
// Expose the standard pure-Go implementation directly to SSA so coroutine
// analysis can propagate its exact effect without an opaque assembly-to-Go
// trampoline.
func block(seed *[4]uint64, blocks *[32]uint64, counter uint32) {
	block_generic(seed, blocks, counter)
}
