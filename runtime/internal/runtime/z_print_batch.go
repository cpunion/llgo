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

import "unsafe"

// PrintArgV1 is the fixed-layout compiler/runtime transport for one operand of
// Go's print or println builtin. Pointer-bearing values remain in typed pointer
// fields so a stackless coroutine frame stays conservatively traceable while
// PrintBatchV1 is suspended. Scalar bits and widened lengths use Word/Extra.
//
// This is an internal physical ABI, not a source-level formatting API.
type PrintArgV1 struct {
	kind    uint8
	pointer unsafe.Pointer
	aux     unsafe.Pointer
	word    uint64
	extra   uint64
}
