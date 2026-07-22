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

import "unsafe"

// RecoverSnapshot is the stable adapter-facing copy returned by TakeRecover.
type RecoverSnapshot struct {
	TypeWord unsafe.Pointer
	DataWord unsafe.Pointer
}

func validRecoverActiveFrame(g *G, child *Frame) bool {
	if !ValidG(g) || child == nil || child != g.active || child.owner != g || child.handle == nil ||
		child.header == nil || child.state != FrameActive || child.header.G != unsafe.Pointer(g) ||
		child.header.SuspendReason != uint16(SuspendNone) ||
		child.header.Lifecycle != uint16(FrameActive) {
		return false
	}
	if child.parent == nil {
		return child == g.root && child.header.Parent == nil
	}
	parent := child.parent
	return parent.owner == g && parent.handle != nil && parent.header != nil &&
		parent.state == FrameSuspended && parent.header.G == unsafe.Pointer(g) &&
		parent.header.SuspendReason == uint16(SuspendCall) &&
		parent.header.Lifecycle == uint16(FrameSuspended) &&
		child.header.Parent == parent.handle && awaitCompletionArmedForChild(child)
}

// TakeRecover implements the predeclared recover operation for an active
// physical coroutine. The direct-call capability is encoded in the immediate
// parent's in-flight CompletionRecord, so no second frame record, TLS lookup,
// or native stack walk is needed. valid is true for every well-formed ordinary
// call that must return nil, including roots, managed helpers, and a second
// recover in the same deferred child.
func TakeRecover(g *G, childHandle unsafe.Pointer) (snapshot RecoverSnapshot, recovered, valid bool) {
	if !ValidG(g) || !resumeGateTaken(g) || childHandle == nil || g.state != GRunning ||
		g.runP == nil || g.pending != (pendingTransition{}) || g.destroyTarget != nil ||
		g.destroyRoot || g.spawnChild != nil || !releasableParkState(&g.park) {
		return RecoverSnapshot{}, false, false
	}
	child := findFrame(g, childHandle)
	if !validRecoverActiveFrame(g, child) {
		return RecoverSnapshot{}, false, false
	}
	if child.parent == nil {
		return RecoverSnapshot{}, false, true
	}
	record := &child.parent.completion
	if record.child != childHandle {
		return RecoverSnapshot{}, false, false
	}
	switch record.status {
	case completionArmed:
		if record.typeWord != nil || record.dataWord != nil {
			return RecoverSnapshot{}, false, false
		}
		return RecoverSnapshot{}, false, true
	case completionRecoverArmed:
		if record.typeWord == nil {
			return RecoverSnapshot{}, false, false
		}
		record.status = completionRecoverTaken
		return RecoverSnapshot{TypeWord: record.typeWord, DataWord: record.dataWord}, true, true
	case completionRecoverTaken:
		if record.typeWord == nil {
			return RecoverSnapshot{}, false, false
		}
		return RecoverSnapshot{}, false, true
	default:
		return RecoverSnapshot{}, false, false
	}
}
