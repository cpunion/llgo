//go:build coro_runtime_adapter_test

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

import "github.com/goplus/llgo/runtime/internal/coro"

type coroProgramTestTargetModeV1 uint8

const (
	coroProgramTestTargetSyncV1 coroProgramTestTargetModeV1 = iota
	coroProgramTestTargetAsyncV1
)

type coroProgramTestTargetStateV1 struct {
	mode                           coroProgramTestTargetModeV1
	handle                         coro.ExecutorHandle
	epoch                          uint32
	started                        bool
	closeCalls                     uint32
	pollCalls                      uint32
	joined                         bool
	waitCalls                      uint32
	waitEpoch                      uint32
	wakePollCalls                  uint32
	wakeReady                      bool
	completeWaitBeforeBeginReturn  bool
	waitBeginDepth                 uint32
	maxWaitBeginDepth              uint32
	completeCloseBeforeBeginReturn bool
	reentrantCloseStatus           coroProgramDriveStatusV1
	closePollEntered               chan struct{}
	closePollRelease               chan struct{}
}

var coroProgramTestTargetV1State coroProgramTestTargetStateV1

func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	state := &coroProgramTestTargetV1State
	if state.started || handle.Slot == 0 || handle.Generation == 0 {
		return false
	}
	state.started = true
	state.handle = handle
	return true
}

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroProgramTestTargetV1State
	if !state.started || state.handle != handle || state.epoch != 0 || state.waitEpoch != 0 || epoch == 0 {
		return coroTargetDispatchInvalidV1
	}
	state.closeCalls++
	state.epoch = epoch
	if state.completeCloseBeforeBeginReturn {
		state.joined = true
		state.reentrantCloseStatus = coroProgramContinueV1(epoch)
	}
	if state.mode == coroProgramTestTargetAsyncV1 {
		return coroTargetDispatchPendingV1
	}
	state.joined = true
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroProgramTestTargetV1State
	if !state.started || state.handle != handle || state.epoch != epoch || epoch == 0 {
		return coroTargetDispatchInvalidV1
	}
	if state.closePollEntered != nil {
		state.closePollEntered <- struct{}{}
		<-state.closePollRelease
	}
	state.pollCalls++
	if !state.joined {
		return coroTargetDispatchPendingV1
	}
	return coroTargetDispatchCompleteV1
}

func coroTargetBeginExecutorWaitV1(handle coro.ExecutorHandle, epoch uint32, deadline int64, hasDeadline bool) coroTargetDispatchResultV1 {
	state := &coroProgramTestTargetV1State
	if !state.started || state.handle != handle || state.waitEpoch != 0 || epoch == 0 || deadline != 0 || hasDeadline {
		return coroTargetDispatchInvalidV1
	}
	state.waitBeginDepth++
	if state.waitBeginDepth > state.maxWaitBeginDepth {
		state.maxWaitBeginDepth = state.waitBeginDepth
	}
	defer func() { state.waitBeginDepth-- }()
	state.waitCalls++
	state.waitEpoch = epoch
	if state.completeWaitBeforeBeginReturn {
		if activeCoroProgramDriver == nil {
			return coroTargetDispatchInvalidV1
		}
		posted := coro.PostWaitAndRequest(
			&coroProgramWaitTableV1State,
			activeCoroProgramDriver.waitRegistration,
			&coroProgramExecutorRegistryV1State,
			handle,
		)
		if posted.Wait != coro.WaitRegistrationPosted || posted.Executor != coro.ExecutorRequestIdleWake {
			return coroTargetDispatchInvalidV1
		}
		state.waitEpoch = 0
		return coroTargetDispatchCompleteV1
	}
	return coroTargetDispatchPendingV1
}

func coroTargetPollExecutorWakeV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroProgramTestTargetV1State
	if !state.started || state.handle != handle || state.waitEpoch != epoch || epoch == 0 {
		return coroTargetDispatchInvalidV1
	}
	state.wakePollCalls++
	if !state.wakeReady {
		return coroTargetDispatchPendingV1
	}
	state.waitEpoch = 0
	return coroTargetDispatchCompleteV1
}
