//go:build !baremetal && (wasm || tinygo.wasm)

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

import _ "unsafe"

const (
	LLGoPackage = "link"
	LLGoFiles   = "_wrap/debugtrap.c"
)

// A stackless WebAssembly instance has one executor unless an embedding
// explicitly provides a parallel scheduler profile. Do not infer host CPUs or
// pull in unistd/signal/unwind process support from the layout-only frontend.
func c_maxprocs() int32 { return 1 }

//go:linkname c_debugtrap C.llgo_debugtrap
//llgo:coro noblock
func c_debugtrap()
