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
	"github.com/goplus/llgo/runtime/internal/clite/time"
	"github.com/goplus/llgo/runtime/internal/runtime/math"
)

// rand and srand mutate only libc's process-local PRNG state. They do not
// perform I/O, wait for a host event, or call back into Go. libc may serialize
// that state with an internal lock, so this is synchronous rather than the
// stronger IRQ-safe noblock contract.
//
//llgo:coro sync
//go:linkname libcRand C.rand
func libcRand() c.Int

//llgo:coro sync
//go:linkname srand C.srand
func srand(uint32)

func fastrand() uint32 {
	return uint32(libcRand())
}

func fastrand64() uint64 {
	n := uint64(fastrand())
	n += 0xa0761d6478bd642f
	hi, lo := math.Mul64(n, n^0xe7037ed1a0b428db)
	return hi ^ lo
}

func init() {
	srand(uint32(time.Time(nil)))
	hashkey[0] = uintptr(fastrand()) | 1
	hashkey[1] = uintptr(fastrand()) | 1
	hashkey[2] = uintptr(fastrand()) | 1
	hashkey[3] = uintptr(fastrand()) | 1
}
