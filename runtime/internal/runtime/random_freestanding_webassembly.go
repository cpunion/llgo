//go:build wasip2 || wasm_unknown

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

// These targets deliberately expose neither wall time nor an entropy import.
// Their host-pull runtime is single-domain, so a deterministic xorshift state
// is sufficient for map perturbation without inventing a blocking host ABI.
// A future entropy capability can seed this state.
var fastRandState uint32 = 0x6d2b79f5

func fastrand() uint32 {
	value := fastRandState
	value ^= value << 13
	value ^= value >> 17
	value ^= value << 5
	fastRandState = value
	return value
}

func fastrand64() uint64 {
	return uint64(fastrand())<<32 | uint64(fastrand())
}

func init() {
	hashkey[0] = uintptr(fastrand()) | 1
	hashkey[1] = uintptr(fastrand()) | 1
	hashkey[2] = uintptr(fastrand()) | 1
	hashkey[3] = uintptr(fastrand()) | 1
}
