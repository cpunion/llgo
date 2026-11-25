//go:build !baremetal

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
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

const (
	mainExitedBit  = uint64(1) << 63
	gCountMask     = mainExitedBit - 1
	goidCacheBatch = uint64(256)
)

func nextGoidBatch() (first, end uint64) {
	// Internal atomic.Add returns the previous value, like LLVM atomicrmw.
	// Reserve (old, old+batch], returning the inclusive start and exclusive end.
	last := atomic.Add(&sched.goidgen, goidCacheBatch) + goidCacheBatch
	return last - goidCacheBatch + 1, last + 1
}

func nextGoid(gp, owner *g) uint64 {
	// Match Go's per-P id allocation shape. The owner is the currently running
	// parent, not the G being initialized. A detached coroutine and bootstrap
	// context have no physical P and use the global fallback.
	if owner != nil {
		mp := owner.m
		if mp != nil && mp.curg == owner {
			pp := mp.p
			if pp != nil && pp.m == mp {
				if pp.goidcache == pp.goidcacheend {
					// Internal atomic.Add returns the previous value, like LLVM
					// atomicrmw. Reserve (old, old+batch], not the preceding batch.
					end := atomic.Add(&sched.goidgen, goidCacheBatch) + goidCacheBatch
					pp.goidcache = end - goidCacheBatch + 1
					pp.goidcacheend = end + 1
				}
				id := pp.goidcache
				pp.goidcache++
				return id
			}
		}
	}
	return atomic.Add(&sched.goidgen, uint64(1)) + 1
}

func nextMid(mp *m) int64 {
	return atomic.Add(&sched.midgen, int64(1)) + 1
}

func nextPid(pp *p) int32 {
	return atomic.Add(&sched.pidgen, int32(1))
}

func coroGCountShardIndex(route uint16) int {
	if route == 0 {
		return 0
	}
	index := int(route - 1)
	if index >= len(coroGCountShards) {
		return index % len(coroGCountShards)
	}
	return index
}

func retainG() {
	if atomic.Add(&sched.gstate, uint64(1))&mainExitedBit != 0 {
		atomic.Add(&sched.gstateSeq, uint64(1))
	}
}

func retainCoroG(route uint16) {
	if atomic.Add(&coroGCountShards[coroGCountShardIndex(route)].state, uint64(1))&mainExitedBit != 0 {
		atomic.Add(&sched.gstateSeq, uint64(1))
	}
}

// releaseG drops one registered context and returns the remaining count and
// main-exited state from the same atomic result.
func releaseG() (remaining uint64, mainExited bool) {
	// llgo.atomicSub follows LLVM atomicrmw and returns the value before the
	// subtraction, unlike sync/atomic.Add. Convert it to the post-release
	// state before deciding whether this was the last context.
	state := atomic.Sub(&sched.gstate, uint64(1)) - 1
	return state & gCountMask, state&mainExitedBit != 0
}

func releaseCoroG(route uint16) {
	atomic.Sub(&coroGCountShards[coroGCountShardIndex(route)].state, uint64(1))
}

func releaseCoroGAndCheckDeadlock(route uint16) {
	state := atomic.Sub(&coroGCountShards[coroGCountShardIndex(route)].state, uint64(1)) - 1
	if state&mainExitedBit == 0 {
		return
	}
	remaining, mainExited := gState()
	if remaining == 0 && mainExited {
		fatal("no goroutines (main called runtime.Goexit) - deadlock!")
		c.Exit(2)
	}
}

func markMainExited() {
	// Mark every shard before sched.gstate so any gState() observing
	// sched.gstate&mainExitedBit != 0 is guaranteed that all prior retains are
	// visible in their shards and all later retains observe mainExitedBit and
	// advance gstateSeq before returning.
	for i := range coroGCountShards {
		atomic.Or(&coroGCountShards[i].state, mainExitedBit)
	}
	atomic.Or(&sched.gstate, mainExitedBit)
}

func gState() (count uint64, mainExited bool) {
	for {
		seq := atomic.Load(&sched.gstateSeq)
		state := atomic.Load(&sched.gstate)
		exited := state&mainExitedBit != 0
		total := int64(state & gCountMask)
		for i := range coroGCountShards {
			shard := atomic.Load(&coroGCountShards[i].state)
			total += int64(shard&gCountMask) - int64(coroGCountShardBias)
		}
		if exited && atomic.Load(&sched.gstateSeq) != seq {
			continue
		}
		if total < 0 {
			total = 0
		}
		return uint64(total), exited
	}
}

func readgstatus(gp *g) uint32 {
	return atomic.Load(&gp.atomicstatus)
}

func setgstatus(gp *g, status uint32) {
	atomic.Store(&gp.atomicstatus, status)
}

func casgstatus(gp *g, oldval, newval uint32) {
	if _, ok := atomic.CompareAndExchange(&gp.atomicstatus, oldval, newval); !ok {
		fatal("runtime: invalid goroutine status transition")
	}
}

func readpstatus(pp *p) uint32 {
	return atomic.Load(&pp.status)
}

func setpstatus(pp *p, status uint32) {
	atomic.Store(&pp.status, status)
}
