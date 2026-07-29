//go:build wasip1 || wasip2 || wasm_unknown

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

	"github.com/goplus/llgo/runtime/internal/runtime/math"
)

var fastRandState uint32 = 0x9e3779b9

func fastrand() uint32 {
	value := fastRandState
	value ^= value << 13
	value ^= value >> 17
	value ^= value << 5
	fastRandState = value
	return value
}

func rand() uint64 {
	n := uint64(fastrand())
	n += 0xa0761d6478bd642f
	hi, lo := math.Mul64(n, n^0xe7037ed1a0b428db)
	return hi ^ lo
}

func randn(n uint32) uint32 {
	return uint32((uint64(uint32(rand())) * uint64(n)) >> 32)
}

//go:linkname os_fastrand os.fastrand
func os_fastrand() uint32 {
	return fastrand()
}

//go:linkname rand_fastrand64 math/rand.fastrand64
func rand_fastrand64() uint64 {
	return rand()
}

//go:linkname sync_fastrandn sync.fastrandn
func sync_fastrandn(n uint32) uint32 {
	return randn(n)
}

//go:linkname net_fastrandu net.fastrandu
func net_fastrandu() uint {
	return uint(fastrand())
}
