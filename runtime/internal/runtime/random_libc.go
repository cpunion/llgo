//go:build !windows && !wasip1 && !wasip2 && !wasm_unknown && !baremetal

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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
	"github.com/xgo-dev/llgo/runtime/internal/clite/time"
	"github.com/xgo-dev/llgo/runtime/internal/runtime/math"
	"github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

var (
	fastrandBaseSeed uint64 = 0x2545f4914f6cdd1d
	fastrandSeed     uint64 = 0x9e3779b97f4a7c15
)

func fastrandMix64(s uint64) uint64 {
	s = (s ^ (s >> 30)) * 0xbf58476d1ce4e5b9
	s = (s ^ (s >> 27)) * 0x94d049bb133111eb
	s ^= s >> 31
	if s == 0 {
		return 0x2545f4914f6cdd1d
	}
	return s
}

func fastrand() uint32 {
	if gp := getgIfPresent(); gp != nil {
		s := uint64(gp.fastrand0) | uint64(gp.fastrand1)<<32
		if s == 0 {
			if gp.goid != 0 {
				s = fastrandMix64((gp.goid * 0x9e3779b97f4a7c15) ^ fastrandBaseSeed)
			} else {
				s = fastrandMix64(atomic.Add(&fastrandSeed, uint64(0x9e3779b97f4a7c15)) ^ fastrandBaseSeed)
			}
		}
		s ^= s >> 12
		s ^= s << 25
		s ^= s >> 27
		gp.fastrand0 = uint32(s)
		gp.fastrand1 = uint32(s >> 32)
		return uint32((s * 0x2545f4914f6cdd1d) >> 32)
	}
	s := fastrandMix64(atomic.Add(&fastrandSeed, uint64(0x9e3779b97f4a7c15)) ^ fastrandBaseSeed)
	return uint32((s * 0x2545f4914f6cdd1d) >> 32)
}

func fastrand64() uint64 {
	n := uint64(fastrand())
	n += 0xa0761d6478bd642f
	hi, lo := math.Mul64(n, n^0xe7037ed1a0b428db)
	return hi ^ lo
}

func init() {
	seed := fastrandMix64(uint64(time.Time(nil)) ^ 0x9e3779b97f4a7c15)
	fastrandBaseSeed = seed
	fastrandSeed = seed ^ 0x9e3779b97f4a7c15
	hashkey[0] = uintptr(fastrand()) | 1
	hashkey[1] = uintptr(fastrand()) | 1
	hashkey[2] = uintptr(fastrand()) | 1
	hashkey[3] = uintptr(fastrand()) | 1
}
