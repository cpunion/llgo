//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	_ "unsafe" // required by go:linkname

	"github.com/goplus/llgo/runtime/internal/coro"
)

const (
	coroPollDescPublishAcceptedV1 uint32 = 1 << iota
	coroPollDescPublishClosingV1
)

//llgo:coro noblock
//go:linkname llgoCoroPollDescPublishOperationV1 C.__llgo_runtime_poll_desc_publish_operation_v1
func llgoCoroPollDescPublishOperationV1(ctx uintptr, interest, sourceSlot, generation uint32) uint32

//llgo:coro noblock
//go:linkname llgoCoroPollDescClearOperationV1 C.__llgo_runtime_poll_desc_clear_operation_v1
func llgoCoroPollDescClearOperationV1(ctx uintptr, interest, sourceSlot, generation uint32) uint32

//llgo:coro noblock
//go:linkname llgoCoroPollDescLoadOperationV1 C.__llgo_runtime_poll_desc_load_operation_v1
func llgoCoroPollDescLoadOperationV1(ctx uintptr, interest uint32) uint64

//llgo:coro noblock
//go:linkname llgoCoroPollDescDeadlineOwnerV1 C.__llgo_runtime_poll_desc_deadline_v1
func llgoCoroPollDescDeadlineOwnerV1(ctx uintptr, mode int32) int64

func coroPollDescPublishOperationV1(
	ctx uintptr,
	interest coro.PollInterest,
	id coro.OperationID,
) (closing bool, ok bool) {
	if ctx == 0 || !id.Valid() || id.Source() != coro.OperationSourcePoll ||
		(interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return false, false
	}
	result := llgoCoroPollDescPublishOperationV1(
		ctx,
		uint32(interest),
		id.SourceSlot,
		id.Generation,
	)
	if result&^(coroPollDescPublishAcceptedV1|coroPollDescPublishClosingV1) != 0 ||
		result&coroPollDescPublishAcceptedV1 == 0 {
		return false, false
	}
	return result&coroPollDescPublishClosingV1 != 0, true
}

func coroPollDescClearOperationV1(
	ctx uintptr,
	interest coro.PollInterest,
	id coro.OperationID,
) bool {
	return ctx != 0 && id.Valid() &&
		llgoCoroPollDescClearOperationV1(
			ctx,
			uint32(interest),
			id.SourceSlot,
			id.Generation,
		) == 1
}

func coroPollDescLoadOperationV1(ctx uintptr, interest coro.PollInterest) (coro.OperationID, bool) {
	if ctx == 0 || (interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return coro.OperationID{}, false
	}
	packed := llgoCoroPollDescLoadOperationV1(ctx, uint32(interest))
	if packed == 0 {
		return coro.OperationID{}, true
	}
	id := coro.OperationID{
		SourceSlot: uint32(packed),
		Generation: uint32(packed >> 32),
	}
	return id, id.Valid() && id.Source() == coro.OperationSourcePoll
}

func coroPollDescDeadlineV1(ctx uintptr, interest coro.PollInterest) (int64, bool) {
	if ctx == 0 {
		return 0, false
	}
	var mode int32
	switch interest {
	case coro.PollInterestRead:
		mode = 'r'
	case coro.PollInterestWrite:
		mode = 'w'
	default:
		return 0, false
	}
	return llgoCoroPollDescDeadlineOwnerV1(ctx, mode), true
}
