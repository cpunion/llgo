//go:build !llgo

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

package build

import (
	stdcontext "context"
	"fmt"
	goimporter "go/importer"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goplus/llgo/cl"
	llssa "github.com/goplus/llgo/ssa"
)

var (
	coroNativeFleetE2ERuntimeArchiveOnce sync.Once
	coroNativeFleetE2ERuntimeArchive     string
)

const coroNativeFleetE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var ChildPasses uint32
var MainStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	ChildPasses = 0
	MainStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	for ChildPasses < 700000 {
		ChildPasses++
	}
	ChildStage = 0x1234abcd
}

func main() {
	MainStage = 1
	go child()
	for ChildStage != 0x1234abcd {
	}
	MainStage = 2
}

func Check() int32 {
	if ChildStage != 0x1234abcd || ChildPasses != 700000 || MainStage != 2 {
		return 17
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 18
	}
	return 0
}
`

const coroNativeFleetForeignReentryE2ESource = `package main

import _ "unsafe"

var Ready chan int32
var Result int32

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname foreignReentry C.__llgo_coro_native_fleet_e2e_reentry_v1
func foreignReentry(func(int32) int32, int32) int32

func Setup() {
	Ready = make(chan int32)
	Result = 0
}

func sender() {
	Ready <- 10
	Ready <- 20
}

func callback(value int32) int32 {
	return value + <-Ready
}

func main() {
	go sender()
	Result = foreignReentry(callback, 1)
}

func Check() int32 {
	if Result != 33 {
		return 71
	}
	return 0
}
`

const coroNativeFleetSameMForeignE2ESource = `package main

import _ "unsafe"

var Start chan int32
var ParentBefore uintptr
var ParentAfter uintptr
var ReplacementThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=caller-thread reentry=none memory=by-value
//go:linkname sameMBlock C.__llgo_coro_native_fleet_e2e_block_v1
func sameMBlock()

func Setup() {
	Start = make(chan int32, 1)
	ParentBefore = 0
	ParentAfter = 0
	ReplacementThread = 0
}

func sender() {
	<-Start
	for isWaiting() == 0 {
	}
	ReplacementThread = threadID()
	unblock()
}

func main() {
	resetState()
	go sender()
	Start <- 1
	ParentBefore = threadID()
	sameMBlock()
	ParentAfter = threadID()
}

func Check() int32 {
	if ParentBefore == 0 || ParentAfter != ParentBefore {
		return 72
	}
	if ReplacementThread == 0 || ReplacementThread == ParentBefore {
		return 73
	}
	return 0
}
`

const coroNativeFleetShutdownE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var MainStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	MainStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	ChildStage = 1
	for {
	}
}

func main() {
	MainStage = 1
	go child()
	for ChildStage == 0 {
	}
	MainStage = 2
}

func Check() int32 {
	if ChildStage != 1 || MainStage != 2 {
		return 27
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 28
	}
	return 0
}
`

const coroNativeFleetPeerSpawnE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var GrandchildStage uint32
var Ack chan uint32
var MainThread uintptr
var ChildThread uintptr
var GrandchildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	GrandchildStage = 0
	Ack = make(chan uint32)
	MainThread = threadID()
	ChildThread = 0
	GrandchildThread = 0
}

func grandchild() {
	thread := threadID()
	if thread != MainThread {
		go grandchild()
		return
	}
	GrandchildThread = thread
	GrandchildStage = 1
	Ack <- 1
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	go grandchild()
	ChildStage = 1
	Ack <- 2
}

func main() {
	go child()
	<-Ack
	<-Ack
}

func Check() int32 {
	if ChildStage != 1 || GrandchildStage != 1 {
		return 37
	}
	if MainThread == 0 || ChildThread == 0 || GrandchildThread == 0 ||
		MainThread == ChildThread || MainThread != GrandchildThread {
		return 38
	}
	return 0
}
`

const coroNativeFleetLockedGExitRetiresPeerE2ESource = `package main

import _ "unsafe"

var Failed uint32
var MainThread uintptr
var RetiredThread uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro sync
//go:linkname armThreadExit C.__llgo_coro_native_fleet_e2e_arm_thread_exit_v1
func armThreadExit() uintptr

//llgo:coro noblock
//go:linkname threadExitCount C.__llgo_coro_native_fleet_e2e_thread_exit_count_v1
func threadExitCount() uintptr

func Setup() {
	Failed = 0
	MainThread = threadID()
	RetiredThread = 0
}

func retireLockedPeer() {
	thread := threadID()
	if thread == MainThread {
		go retireLockedPeer()
		return
	}
	RetiredThread = thread
	osThreadLock()
	if armThreadExit() != 1 {
		Failed = 41
	}
	// Deliberately do not call UnlockOSThread. Completion must replace this M
	// before publishing the G as reclaimable.
}

func main() {
	go retireLockedPeer()
	for threadExitCount() == 0 {
	}
	if RetiredThread == 0 || RetiredThread == MainThread {
		Failed = 42
	}
}

func Check() int32 {
	if Failed != 0 {
		return int32(Failed)
	}
	if threadExitCount() != 1 {
		return 43
	}
	return 0
}
`

const coroNativeFleetLockedGExitRetiresProgramE2ESource = `package main

import _ "unsafe"

var Started chan uint32
var MainThread uintptr
var RetiredThread uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro sync
//go:linkname armThreadExit C.__llgo_coro_native_fleet_e2e_arm_thread_exit_v1
func armThreadExit() uintptr

//llgo:coro noblock
//go:linkname threadExitCount C.__llgo_coro_native_fleet_e2e_thread_exit_count_v1
func threadExitCount() uintptr

func Setup() {
	Started = make(chan uint32, 1)
	MainThread = threadID()
	RetiredThread = 0
}

func retireProgramOwner() {
	thread := threadID()
	if thread != MainThread {
		go retireProgramOwner()
		return
	}
	RetiredThread = thread
	osThreadLock()
	if armThreadExit() != 1 {
		for {
		}
	}
	Started <- 1
	// The buffered send wakes main without parking this locked G. The old
	// program pthread must exit before the awakened main can continue.
}

func main() {
	go retireProgramOwner()
	<-Started
	for threadExitCount() == 0 {
	}
	if RetiredThread != MainThread || threadID() == MainThread {
		for {
		}
	}
}

// The original C driver thread is deliberately retired and therefore cannot
// call Check. Process success comes only from the clean program successor
// resuming main and completing the runtime close path.
func Check() int32 {
	return 51
}
`

const coroNativeFleetBlockedLockedGMainReturnE2ESource = `package main

import _ "unsafe"

var MainThread uintptr
var BlockedThread uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

func Setup() {
	MainThread = threadID()
	BlockedThread = 0
}

func blockedLockedChild() {
	thread := threadID()
	if thread == MainThread {
		go blockedLockedChild()
		return
	}
	BlockedThread = thread
	osThreadLock()
	block()
}

func main() {
	resetState()
	go blockedLockedChild()
	for isWaiting() == 0 {
	}
	if BlockedThread == 0 || BlockedThread == MainThread {
		for {
		}
	}
	// Go main return must terminate the process; it cannot wait to join an M
	// which remains inside an uninterruptible foreign call.
}

func Check() int32 {
	return 61
}
`

const coroNativeFleetChannelSelectE2ESource = `package main

import _ "unsafe"

var Data chan uint32
var Never chan uint32
var Ack chan uint32
var Got uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	Data = make(chan uint32)
	Never = make(chan uint32)
	Ack = make(chan uint32)
	Got = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	select {
	case Got = <-Data:
	case Got = <-Never:
	}
	Ack <- 1
}

func main() {
	go child()
	Data <- 0xdecafbad
	<-Ack
}

func Check() int32 {
	if Got != 0xdecafbad {
		return 47
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 48
	}
	return 0
}
`

const coroNativeFleetChannelShutdownE2ESource = `package main

import _ "unsafe"

var NeverA chan uint32
var NeverB chan uint32
var ChildStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	NeverA = make(chan uint32)
	NeverB = make(chan uint32)
	ChildStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	ChildStage = 1
	select {
	case <-NeverA:
	case <-NeverB:
	}
	ChildStage = 2
}

func main() {
	go child()
	for ChildStage == 0 {
	}
}

func Check() int32 {
	if ChildStage != 1 {
		return 57
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 58
	}
	return 0
}
`

const coroNativeFleetSingleExecutionQuotaE2ESource = `package main

var Data chan uint32
var Got uint32

func Setup() {
	Data = make(chan uint32)
	Got = 0
}

func child() {
	Data <- 0x51a91e
}

func main() {
	go child()
	Got = <-Data
}

func Check() int32 {
	if Got != 0x51a91e {
		return 67
	}
	return 0
}
`

const coroNativeFleetGOMAXPROCSE2ESource = `package main

import _ "unsafe"

var Done chan uint32
var Failed uint32

//llgo:coro noblock
//go:linkname gomaxprocs command-line-arguments.CoroGOMAXPROCS
func gomaxprocs(int) int

//llgo:coro noblock
//go:linkname quotaReset C.__llgo_coro_native_fleet_e2e_quota_reset_v1
func quotaReset()

//llgo:coro noblock
//go:linkname quotaRun C.__llgo_coro_native_fleet_e2e_quota_run_v1
func quotaRun(uint32)

//llgo:coro noblock
//go:linkname quotaMaximum C.__llgo_coro_native_fleet_e2e_quota_maximum_v1
func quotaMaximum() uint32

func Setup() {
	Done = make(chan uint32)
	Failed = 0
}

func child() {
	quotaRun(12000000)
	Done <- 1
}

func runPhase() uint32 {
	quotaReset()
	for index := 0; index < 8; index++ {
		go child()
	}
	for index := 0; index < 8; index++ {
		<-Done
	}
	return quotaMaximum()
}

func main() {
	if gomaxprocs(0) != 1 {
		Failed = 71
		return
	}
	if gomaxprocs(4) != 1 || gomaxprocs(0) != 4 {
		Failed = 72
		return
	}
	maximum := runPhase()
	if maximum < 2 || maximum > 4 {
		Failed = 73
		return
	}
	if gomaxprocs(1) != 4 || gomaxprocs(0) != 1 {
		Failed = 74
		return
	}
	if maximum = runPhase(); maximum != 1 {
		Failed = 75
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetLockedForeignCompensationE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Start chan uint32

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

func child() {
	<-Start
	for isWaiting() == 0 {
	}
	unblock()
}

func Setup() {
	Failed = 0
	Start = make(chan uint32)
}

func main() {
	resetState()
	go child()
	// The rendezvous creates a stable stack-cut boundary and establishes the
	// child on a peer route before this G enters same-M C.
	Start <- 1
	osThreadLock()
	block()
	osThreadUnlock()
	if isWaiting() == 0 {
		Failed = 81
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetSameRouteReplacementE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Ready chan uint32
var Signal chan uint32
var Never chan uint32
var MainThread uintptr
var WaiterBefore uintptr
var WaiterAfter uintptr
var Got uint32

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

func waiter() {
	thread := threadID()
	// Preserve a real backedge in the fixture so the coroutine plan carries
	// the same preemption contract as an ordinary long-running goroutine.
	for spin := uint32(0); spin < Got; spin++ {
		WaiterBefore = thread
	}
	if thread != MainThread {
		go waiter()
		return
	}
	WaiterBefore = thread
	Ready <- 1
	select {
	case Got = <-Signal:
	case Got = <-Never:
		Failed = 94
		return
	}
	WaiterAfter = threadID()
	unblock()
}

func sender() {
	for isWaiting() == 0 {
	}
	Signal <- 1
}

func Setup() {
	Failed = 0
	Ready = make(chan uint32)
	Signal = make(chan uint32)
	Never = make(chan uint32)
	MainThread = threadID()
	WaiterBefore = 0
	WaiterAfter = 0
	Got = 0
}

func main() {
	resetState()
	go waiter()
	<-Ready
	go sender()
	parentThread := threadID()
	osThreadLock()
	block()
	osThreadUnlock()
	if isWaiting() == 0 {
		Failed = 91
		return
	}
	if threadID() != parentThread {
		Failed = 92
		return
	}
	if WaiterBefore != MainThread || WaiterAfter == 0 || Got != 1 {
		Failed = 93
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetLockedOrdinarySuspendE2ESource = `package main

import _ "unsafe"

var Failed uint32
var MainThread uintptr
var PeerThread uintptr
var YieldReady chan uint32
var YieldStart chan uint32
var YieldDone chan uint32
var ChannelReady chan uint32
var ChannelStart chan uint32
var ChannelValue chan uint32
var TimerReady chan uint32
var TimerStart chan uint32
var TimerDone chan uint32

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//go:linkname schedulerYield llgo.coroYield
func schedulerYield()

//go:linkname timerSleep llgo.coroTimerSleep
func timerSleep(delay int64)

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	Failed = 0
	MainThread = threadID()
	PeerThread = 0
	YieldReady = make(chan uint32)
	YieldStart = make(chan uint32)
	YieldDone = make(chan uint32)
	ChannelReady = make(chan uint32)
	ChannelStart = make(chan uint32)
	ChannelValue = make(chan uint32)
	TimerReady = make(chan uint32)
	TimerStart = make(chan uint32)
	TimerDone = make(chan uint32)
}

func yieldPeer() {
	if threadID() != MainThread {
		go yieldPeer()
		return
	}
	YieldReady <- 1
	<-YieldStart
	PeerThread = threadID()
	YieldDone <- 1
}

func channelPeer() {
	if threadID() != MainThread {
		go channelPeer()
		return
	}
	ChannelReady <- 1
	<-ChannelStart
	PeerThread = threadID()
	ChannelValue <- 37
}

func timerPeer() {
	if threadID() != MainThread {
		go timerPeer()
		return
	}
	TimerReady <- 1
	<-TimerStart
	PeerThread = threadID()
	TimerDone <- 1
}

func testLockedYield() {
	PeerThread = 0
	go yieldPeer()
	<-YieldReady
	before := threadID()
	osThreadLock()
	YieldStart <- 1
	schedulerYield()
	after := threadID()
	<-YieldDone
	osThreadUnlock()
	if before != MainThread || after != before ||
		PeerThread == 0 || PeerThread == before {
		Failed = 141
	}
}

func testLockedChannelPark() {
	PeerThread = 0
	go channelPeer()
	<-ChannelReady
	before := threadID()
	osThreadLock()
	ChannelStart <- 1
	got := <-ChannelValue
	after := threadID()
	osThreadUnlock()
	if got != 37 || before != MainThread || after != before ||
		PeerThread == 0 || PeerThread == before {
		Failed = 142
	}
}

func testLockedTimerPark() {
	PeerThread = 0
	go timerPeer()
	<-TimerReady
	before := threadID()
	osThreadLock()
	TimerStart <- 1
	timerSleep(20 * 1000 * 1000)
	after := threadID()
	<-TimerDone
	osThreadUnlock()
	if before != MainThread || after != before ||
		PeerThread == 0 || PeerThread == before {
		Failed = 143
	}
}

func main() {
	testLockedYield()
	if Failed == 0 {
		testLockedChannelPark()
	}
	if Failed == 0 {
		testLockedTimerPark()
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetStandbyMSetMaxThreadsE2ESource = `package main

import _ "unsafe"

var Failed uint32
var MainThread uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//llgo:coro noblock
//go:linkname setMaxThreads command-line-arguments.CoroSetMaxThreads
func setMaxThreads(threads int) int

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro worker
//go:linkname blockNested C.__llgo_coro_native_fleet_e2e_nested_block_v1
func blockNested()

func Setup() {
	Failed = 0
	MainThread = threadID()
}

func main() {
	// The native fleet owns 13 baseline threads:
	// program + clean factory + 4 workers + 7 peer owners.
	// One temporary replacement fills a limit of 14. The second call can
	// succeed only by reusing that returned physical M from standby.
	if previous := setMaxThreads(14); previous != 10_000 {
		Failed = 141
		return
	}
	for iteration := uint32(0); iteration < 2; iteration++ {
		osThreadLock()
		before := threadID()
		blockNested()
		after := threadID()
		osThreadUnlock()
		if before != MainThread || after != before {
			Failed = 142 + iteration
			return
		}
	}
	if previous := setMaxThreads(14); previous != 14 {
		Failed = 144
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetSameRouteTimerReplacementE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Ready chan uint32
var MainThread uintptr
var WaiterBefore uintptr
var WaiterAfter uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//go:linkname timerSleep llgo.coroTimerSleep
func timerSleep(delay int64)

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

func timerWaiter() {
	thread := threadID()
	if thread != MainThread {
		go timerWaiter()
		return
	}
	WaiterBefore = thread
	Ready <- 1
	timerSleep(30 * 1000 * 1000)
	WaiterAfter = threadID()
	if isWaiting() == 0 {
		Failed = 101
		return
	}
	unblock()
}

func Setup() {
	Failed = 0
	Ready = make(chan uint32)
	MainThread = threadID()
	WaiterBefore = 0
	WaiterAfter = 0
}

func main() {
	resetState()
	go timerWaiter()
	<-Ready
	parentThread := threadID()
	osThreadLock()
	block()
	osThreadUnlock()
	if isWaiting() == 0 {
		Failed = 102
		return
	}
	if threadID() != parentThread {
		Failed = 103
		return
	}
	if WaiterBefore != MainThread || WaiterAfter == 0 ||
		WaiterAfter == MainThread {
		Failed = 104
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetNestedSameRouteReplacementE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Ready chan uint32
var Start chan uint32
var MainThread uintptr
var NestedBefore uintptr
var NestedAfter uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

//llgo:coro noblock
//go:linkname isNestedWaiting C.__llgo_coro_native_fleet_e2e_nested_blocked_v1
func isNestedWaiting() uintptr

//llgo:coro worker
//go:linkname blockNested C.__llgo_coro_native_fleet_e2e_nested_block_v1
func blockNested()

func nestedOwner() {
	thread := threadID()
	if thread != MainThread {
		go nestedOwner()
		return
	}
	Ready <- 2
	<-Start
	osThreadLock()
	NestedBefore = threadID()
	blockNested()
	NestedAfter = threadID()
	osThreadUnlock()
	unblock()
}

func starter() {
	for isWaiting() == 0 {
	}
	Start <- 1
}

func Setup() {
	Failed = 0
	Ready = make(chan uint32)
	Start = make(chan uint32)
	MainThread = threadID()
	NestedBefore = 0
	NestedAfter = 0
}

func main() {
	resetState()
	go nestedOwner()
	<-Ready
	go starter()
	parentThread := threadID()
	osThreadLock()
	block()
	osThreadUnlock()
	if isWaiting() == 0 || isNestedWaiting() == 0 {
		Failed = 111
		return
	}
	if threadID() != parentThread || parentThread != MainThread {
		Failed = 112
		return
	}
	if NestedBefore == 0 || NestedBefore == MainThread ||
		NestedAfter != NestedBefore {
		Failed = 113
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetReplacementRetirementE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Ready chan uint32
var Start chan uint32
var MainThread uintptr
var ReplacementThread uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

//llgo:coro sync
//go:linkname armThreadExit C.__llgo_coro_native_fleet_e2e_arm_thread_exit_v1
func armThreadExit() uintptr

//llgo:coro noblock
//go:linkname threadExitCount C.__llgo_coro_native_fleet_e2e_thread_exit_count_v1
func threadExitCount() uintptr

//llgo:coro worker
//go:linkname waitExitAndRelease C.__llgo_coro_native_fleet_e2e_wait_exit_and_release_v1
func waitExitAndRelease()

func retireReplacement() {
	if threadID() != MainThread {
		go retireReplacement()
		return
	}
	Ready <- 1
	<-Start
	ReplacementThread = threadID()
	if ReplacementThread == MainThread {
		Failed = 131
		unblock()
		return
	}
	osThreadLock()
	if armThreadExit() != 1 {
		Failed = 132
		unblock()
	}
	// The replacement G exits with its lock held. Its M must hand the same
	// parent baton to a clean successor before the TLS destructor can run.
}

func releaseParent() {
	for isWaiting() == 0 {
	}
	Start <- 1
	waitExitAndRelease()
}

func Setup() {
	Failed = 0
	Ready = make(chan uint32)
	Start = make(chan uint32, 1)
	MainThread = threadID()
	ReplacementThread = 0
}

func main() {
	resetState()
	go retireReplacement()
	<-Ready
	go releaseParent()
	parentThread := threadID()
	osThreadLock()
	block()
	osThreadUnlock()
	if threadID() != parentThread || parentThread != MainThread {
		Failed = 133
	}
	if threadExitCount() != 1 || ReplacementThread == 0 ||
		ReplacementThread == MainThread {
		Failed = 134
	}
}

func Check() int32 {
	return int32(Failed)
}
`

const coroNativeFleetSameRoutePollReplacementE2ESource = `package main

import _ "unsafe"

var Failed uint32
var Ready chan uint32
var PollContext uintptr
var ReadFD int32
var MainThread uintptr
var WaiterBefore uintptr
var WaiterAfter uintptr

//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()

//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()

//go:linkname pollWait llgo.coroPollWait
func pollWait(context uintptr, fd int32, interest uint32, deadline int64) uint32

//go:linkname pollAlloc C.__llgo_runtime_poll_desc_alloc_v1
func pollAlloc(fd int32, inlineStream uint32) uintptr

//go:linkname pollFree C.__llgo_runtime_poll_desc_free_v1
func pollFree(context uintptr)

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()

//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr

//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()

//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()

//llgo:coro sync
//go:linkname streamReset C.__llgo_coro_native_fleet_e2e_stream_reset_v1
func streamReset() uintptr

//llgo:coro noblock
//go:linkname streamReadFD C.__llgo_coro_native_fleet_e2e_stream_read_fd_v1
func streamReadFD() int32

//llgo:coro noblock
//go:linkname streamWrite C.__llgo_coro_native_fleet_e2e_stream_write_v1
func streamWrite() uintptr

//llgo:coro noblock
//go:linkname streamRead C.__llgo_coro_native_fleet_e2e_stream_read_v1
func streamRead() uintptr

//llgo:coro sync
//go:linkname streamClose C.__llgo_coro_native_fleet_e2e_stream_close_v1
func streamClose()

func pollWaiter() {
	thread := threadID()
	if thread != MainThread {
		go pollWaiter()
		return
	}
	WaiterBefore = thread
	Ready <- 1
	if status := pollWait(PollContext, ReadFD, 1, 0); status != 1 {
		Failed = 121
		unblock()
		return
	}
	if value := streamRead(); value != 0x5a {
		Failed = 122
		unblock()
		return
	}
	WaiterAfter = threadID()
	unblock()
}

func sender() {
	for isWaiting() == 0 {
	}
	if streamWrite() != 1 {
		Failed = 123
		unblock()
	}
}

func Setup() {
	Failed = 0
	Ready = make(chan uint32)
	PollContext = 0
	ReadFD = -1
	MainThread = threadID()
	WaiterBefore = 0
	WaiterAfter = 0
	if streamReset() != 1 {
		Failed = 124
		return
	}
	ReadFD = streamReadFD()
	if ReadFD < 0 {
		Failed = 125
	}
}

func runPoll() {
	resetState()
	go pollWaiter()
	<-Ready
	go sender()
	parentThread := threadID()
	osThreadLock()
	block()
	osThreadUnlock()
	if isWaiting() == 0 {
		Failed = 126
		return
	}
	if threadID() != parentThread || parentThread != MainThread {
		Failed = 127
		return
	}
	if WaiterBefore != MainThread || WaiterAfter == 0 ||
		WaiterAfter == MainThread {
		Failed = 128
	}
}

func main() {
	if Failed != 0 {
		return
	}
	PollContext = pollAlloc(ReadFD, 1)
	if PollContext == 0 {
		Failed = 125
		return
	}
	runPoll()
	if PollContext != 0 {
		pollFree(PollContext)
		PollContext = 0
	}
}

func Check() int32 {
	result := Failed
	streamClose()
	return int32(result)
}
`

func TestCoroNativeFleetPhysicalPeerRunsDistributedChildE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetE2ESource, "distributed-child", false, 8)
}

func TestCoroNativeFleetManagedForeignReentryParksCallbackE2E(t *testing.T) {
	runCoroNativeFleetE2E(
		t,
		coroNativeFleetForeignReentryE2ESource,
		"managed-foreign-reentry",
		true,
		1,
	)
}

func TestCoroNativeFleetSameMForeignKeepsSchedulerProgressE2E(t *testing.T) {
	runCoroNativeFleetE2E(
		t,
		coroNativeFleetSameMForeignE2ESource,
		"same-m-foreign",
		true,
		1,
	)
}

func TestCoroNativeFleetMainReturnCancelsPeerE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetShutdownE2ESource, "main-return-cancel", false, 4)
}

func TestCoroNativeFleetPeerSpawnReturnsToProgramE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetPeerSpawnE2ESource, "peer-spawn-program", true, 4)
}

func TestCoroNativeFleetLockedGExitRetiresPhysicalPeerE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetLockedGExitRetiresPeerE2ESource, "locked-g-exit-retire-peer", false, 1)
}

func TestCoroNativeFleetLockedGExitRetiresProgramOwnerE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetLockedGExitRetiresProgramE2ESource, "locked-g-exit-retire-program", true, 1)
}

func TestCoroNativeFleetMainReturnDoesNotJoinBlockedLockedGE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetBlockedLockedGMainReturnE2ESource, "blocked-locked-g-main-return", false, 2)
}

func TestCoroNativeFleetChannelSelectCrossRouteE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetChannelSelectE2ESource, "channel-select-cross-route", true, 4)
}

func TestCoroNativeFleetShutdownCancelsPeerChannelSelectE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetChannelShutdownE2ESource, "channel-select-shutdown", true, 4)
}

func TestCoroNativeFleetSingleExecutionQuotaChannelProgressE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetSingleExecutionQuotaE2ESource, "single-quota-channel", true, 1)
}

func TestCoroNativeFleetRuntimeGOMAXPROCSQuotaE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetGOMAXPROCSE2ESource, "runtime-gomaxprocs-quota", true, 1)
}

func TestCoroNativeFleetLockedForeignReleasesQuotaBeforeReplacementStarts(t *testing.T) {
	path := filepath.Join(
		"..", "..", "runtime", "internal", "runtime",
		"coro_os_thread_foreign_llgo.go",
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read locked-thread foreign owner:", err)
	}
	source := string(raw)
	release := strings.Index(
		source,
		"if releaseManaged && !coroTargetReleaseManagedExecutionV1(boundary.driver)",
	)
	create := strings.Index(
		source,
		"if !coroNativeMStartPhysicalOwnerV1(replacement, slot)",
	)
	if release < 0 || create < 0 || release >= create {
		t.Fatalf(
			"locked-thread foreign owner must release its route quota before the replacement pthread can start: release=%d create=%d",
			release,
			create,
		)
	}
}

func TestCoroNativeFleetLockedForeignCompensationE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetLockedForeignCompensationE2ESource, "locked-foreign-compensation", true, 1)
}

func TestCoroNativeFleetSameRouteReplacementE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetSameRouteReplacementE2ESource, "same-route-replacement", true, 1)
}

func TestCoroNativeFleetLockedOrdinarySuspendE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetLockedOrdinarySuspendE2ESource, "locked-ordinary-suspend", true, 1)
}

func TestCoroNativeFleetStandbyMHonorsSetMaxThreadsE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetStandbyMSetMaxThreadsE2ESource, "standby-m-setmaxthreads", false, 1)
}

func TestCoroNativeFleetSameRouteTimerReplacementE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetSameRouteTimerReplacementE2ESource, "same-route-timer-replacement", true, 1)
}

func TestCoroNativeFleetNestedSameRouteReplacementE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetNestedSameRouteReplacementE2ESource, "nested-same-route-replacement", true, 1)
}

func TestCoroNativeFleetReplacementOwnerRetirementE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetReplacementRetirementE2ESource, "replacement-owner-retirement", true, 1)
}

func TestCoroNativeFleetSameRoutePollReplacementE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetSameRoutePollReplacementE2ESource, "same-route-poll-replacement", true, 1)
}

func runCoroNativeFleetE2E(t *testing.T, source, name string, enableChannel bool, initialLimit uint32) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine fleet link smoke requires Darwin or Linux")
	}
	if initialLimit == 0 {
		t.Fatal("native coroutine fleet E2E initial execution limit must be positive")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ar, err := exec.LookPath("llvm-ar")
	if err != nil {
		ar, err = exec.LookPath("ar")
		if err != nil {
			t.Skip("llvm-ar/ar is unavailable")
		}
	}

	llssa.Initialize(llssa.InitAll)
	temp := t.TempDir()
	prog := llssa.NewProgram(nil)
	prog.SetRuntime(func() *types.Package {
		rt, err := goimporter.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime type model:", err)
		}
		for _, typeName := range []string{
			"CoroWorkerParkV1",
			"CoroTimerParkV2",
			"CoroPollParkV2",
		} {
			if rt.Scope().Lookup(typeName) != nil {
				continue
			}
			name := types.NewTypeName(token.NoPos, rt, typeName, nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := rt.Scope().Insert(name); previous != nil {
				t.Fatalf("install %s frame-storage type: duplicate %v", typeName, previous)
			}
		}
		return rt
	})
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	defer prog.Dispose()

	userObject, anchor, setupSymbol, checkSymbol := buildCoroSpawnNativeE2EUserSource(
		t, prog, temp, source, enableChannel, cl.CoroNativeTargetCapabilities(),
	)
	entryObject := buildCoroSpawnNativeE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroSpawnNativeE2EDriver(t, prog, temp, setupSymbol, checkSymbol)
	runtimeArchive := cachedCoroNativeFleetE2ERuntimeArchive(t, clang, ar)

	executable := filepath.Join(temp, "coro-native-fleet-"+name+"-e2e")
	// Keep an opt-in stable output path for inspecting a failed native fleet
	// executable with the platform debugger. Ordinary CI never sets it.
	if diagnostic := strings.TrimSpace(os.Getenv("LLGO_CORO_NATIVE_FLEET_OUTPUT")); diagnostic != "" {
		executable = diagnostic
	}
	linkArgs := []string{driverObject, entryObject, userObject, runtimeArchive, "-pthread", "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine fleet smoke: %v\n%s", err, output)
	}

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, executable)
	command.Env = make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GOMAXPROCS=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, fmt.Sprintf("GOMAXPROCS=%d", initialLimit))
	output, err := command.CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine fleet smoke timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine fleet smoke failed: %v\n%s", err, output)
	}
}

func cachedCoroNativeFleetE2ERuntimeArchive(t *testing.T, clang, ar string) string {
	t.Helper()
	coroNativeFleetE2ERuntimeArchiveOnce.Do(func() {
		// The runtime island and fixed C boundary are identical for every
		// program in this suite. Keep their archive under TestMain's shared
		// cache root so all E2Es pay the production-runtime compile once while
		// retaining separate user, entry, driver, link, and execution checks.
		temp := filepath.Join(cacheRootFunc(), "coro-native-fleet-e2e-runtime")
		if err := os.MkdirAll(temp, 0o755); err != nil {
			t.Fatal("create shared native fleet runtime directory:", err)
		}
		runtimeObjects := buildCoroNativeFleetE2ERuntimeIsland(t, temp)
		runtimeObjects = append(runtimeObjects, buildCoroNativeFleetE2EBoundaryObject(t, clang, temp))
		archive := filepath.Join(temp, "libllgo-coro-fleet-runtime-island.a")
		arArgs := append([]string{"rcs", archive}, runtimeObjects...)
		if output, err := exec.Command(ar, arArgs...).CombinedOutput(); err != nil {
			t.Fatalf("archive coroutine fleet runtime island: %v\n%s", err, output)
		}
		coroNativeFleetE2ERuntimeArchive = archive
	})
	if coroNativeFleetE2ERuntimeArchive == "" {
		t.Fatal("shared native fleet runtime archive is unavailable")
	}
	return coroNativeFleetE2ERuntimeArchive
}

func buildCoroNativeFleetE2EBoundaryObject(t *testing.T, clang, temp string) string {
	t.Helper()
	source := filepath.Join("testdata", "coro_native_fleet_e2e.c")
	object := filepath.Join(temp, "coro-native-fleet-e2e-boundary.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-pthread", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine fleet E2E boundary: %v\n%s", err, output)
	}
	return object
}

func buildCoroNativeFleetE2ERuntimeIsland(t *testing.T, temp string) []string {
	t.Helper()
	files := append(coroNativeTaskContextRuntimeSources(), []string{
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_allocator.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_abort_libc.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_frame.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_program.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_decision.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_slice.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_execution_quota_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_physical_thread_capacity_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_sched.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor_driver_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_nil_fault.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_panic_payload.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_panic_trace_release.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_spawn.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_atomic_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_program_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_reactor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_m_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_replacement_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_replacement_reactor_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_ready_distribution_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_native_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_wait_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_timer_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_os_thread_affinity.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_os_thread_foreign_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_poll_descriptor_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_poll_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_poll_route_native_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_completion_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_resume_materialize.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro_atomic_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_wait_coro.go"),
	}...)
	files = materializeCoroChannelNativeE2ERuntimeIsland(t, files)
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	conf.compilerBuildTags = []string{
		"llgo_coro",
		coroNativePipeBuildTag,
		coroNativeTimerBuildTag,
	}
	configureCoroRuntimeIslandPlan(conf, "NewChan")
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/clite/tls":    true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/coroclock":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
		"github.com/goplus/llgo/runtime/internal/corofleet":    true,
		"github.com/goplus/llgo/runtime/internal/corotimer":    true,
		"github.com/goplus/llgo/runtime/internal/coroworker":   true,
		"github.com/goplus/llgo/runtime/internal/runtime/math": true,
	}
	seen := make(map[string]bool, len(allowed))
	var objects []string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.LPkg.Prog == nil || !allowed[pkg.ID] {
			return
		}
		if seen[pkg.ID] {
			t.Fatalf("native fleet runtime emitted duplicate module %q", pkg.ID)
		}
		seen[pkg.ID] = true
		module := pkg.LPkg.Module()
		if module.IsNil() {
			return
		}
		name := fmt.Sprintf("fleet-runtime-%03d-%s.o", len(objects), sanitizeCoroSpawnNativeE2EObjectName(pkg.ID))
		objects = append(objects, emitCoroSpawnNativeE2EObject(t, pkg.LPkg.Prog, module, filepath.Join(temp, name)))
	}
	pkgs, err := Do(files, conf)
	if err != nil {
		t.Fatalf("compile native fleet production runtime island: %v", err)
	}
	if len(pkgs) == 0 || pkgs[0].LPkg == nil {
		t.Fatal("native fleet production runtime island produced no root package")
	}
	prog := pkgs[0].LPkg.Prog
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("native fleet runtime did not emit required module %q", id)
		}
	}
	objects = append(objects,
		buildCoroRuntimeIslandFaultStringStubs(t, prog, temp),
		buildCoroNativeWorkerCallObject(t, temp),
		buildCoroNativeDoorbellObject(t, temp),
		buildCoroNativePollObject(t, temp),
		buildCoroNativeFleetOwnerObject(t, temp),
	)
	prog.Dispose()
	if len(objects) != len(allowed)+5 {
		t.Fatalf("native fleet runtime objects = %d, want exactly %d package objects plus fault-string, worker, doorbell, poll, and fleet-owner leaves", len(objects), len(allowed))
	}
	return objects
}
