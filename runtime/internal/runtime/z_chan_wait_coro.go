//go:build (llgo && llgo_coro) || coro_channel_owner_test

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

// channelWaitMutex and channelWaitCond make the old pthread-backed waiter path
// unrepresentable in a stackless runtime. Direct channel and select blocking is
// lowered to compiler-spilled CoroChanParkV1/CoroChanSelectV1 state; no managed
// task may acquire a native mutex and later resume on a different worker.
//
// Keep the method surface so the shared nonblocking hchan implementation stays
// compact. Every operation fails closed if an obsolete runtime entry reaches
// this path, and—critically—none of these methods carries a schedulerwait edge
// into the managed SSA plan.
type channelWaitMutex struct{}
type channelWaitCond struct{}

func (*channelWaitMutex) Init(*struct{}) int32 {
	coroRuntimeAbort("legacy pthread channel waiter initialized in coroutine runtime")
	return -1
}

func (*channelWaitMutex) Lock() {
	coroRuntimeAbort("legacy pthread channel waiter locked in coroutine runtime")
}

func (*channelWaitMutex) Unlock() {
	coroRuntimeAbort("legacy pthread channel waiter unlocked in coroutine runtime")
}

func (*channelWaitMutex) Destroy() {
	coroRuntimeAbort("legacy pthread channel waiter destroyed in coroutine runtime")
}

func (*channelWaitCond) Init(*struct{}) int32 {
	coroRuntimeAbort("legacy pthread channel condition initialized in coroutine runtime")
	return -1
}

func (*channelWaitCond) Wait(*channelWaitMutex) int32 {
	coroRuntimeAbort("legacy pthread channel condition waited in coroutine runtime")
	return -1
}

func (*channelWaitCond) Signal() int32 {
	coroRuntimeAbort("legacy pthread channel condition signaled in coroutine runtime")
	return -1
}

func (*channelWaitCond) Destroy() {
	coroRuntimeAbort("legacy pthread channel condition destroyed in coroutine runtime")
}
