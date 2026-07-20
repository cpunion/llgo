//go:build llgo && (baremetal || (!darwin && !linux) || (!amd64 && !arm64))

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

package coro

// Alignment alone does not make an i64 atomic executor-safe. WASM, 32-bit,
// bare-metal and every other unlisted target remain disabled until their
// lowering is certified lock-free or is backed by a bounded IRQ critical
// section. In particular, an unbounded __atomic_*_8 lock fallback is not a
// valid implementation of this capability.
const nonblockingLeaseAtomic64Bounded = false

// These stubs are an additional physical boundary behind the public entry
// checks. They deliberately emit no i64 atomic operation: an unsupported
// target cannot accidentally acquire a lease merely by bypassing an owner
// wrapper inside this package.
func preemptLoad64(_ *uint64) uint64 {
	return 0
}

func preemptStore64(_ *uint64, _ uint64) {
}

func preemptCompareAndSwap64(_ *uint64, _, _ uint64) bool {
	return false
}
