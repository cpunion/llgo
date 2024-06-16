/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
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

package atomic

import (
	_ "unsafe"
)

const (
	LLGoPackage = true
)

//go:linkname cAddInt64 llgo.atomicAdd
func cAddInt64(addr *int64, delta int64) (old int64)

func AddInt64(addr *int64, delta int64) (new int64) {
	return cAddInt64(addr, delta) + delta
}