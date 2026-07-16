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
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// These compiler-owned C ABI wrappers are emitted in the program entry module.
// They hide LLVM's post-CoroSplit handle layout from the Go runtime.

//go:linkname coroHandleDone C.__llgo_coro_done_v1
func coroHandleDone(unsafe.Pointer) bool

//go:linkname coroHandleResume C.__llgo_coro_resume_v1
func coroHandleResume(unsafe.Pointer)

//go:linkname coroHandleDestroy C.__llgo_coro_destroy_v1
func coroHandleDestroy(unsafe.Pointer)

// Keep the runtime-facing names local while the target-neutral implementation
// remains independently testable.
type coroG = coro.G
type coroP = coro.P

func coroInitG(g *coroG) bool {
	return coro.InitG(g)
}

func coroAdoptRoot(g *coroG, handle unsafe.Pointer) bool {
	return coro.AdoptRoot(g, handle)
}

func coroEnqueue(p *coroP, g *coroG) bool {
	return coro.Enqueue(p, g)
}

func coroRunG(p *coroP, g *coroG) bool {
	action, ok := coro.BeginRunG(p, g)
	if !ok {
		return false
	}
	return coroRunActions(p, g, action)
}

func coroRun(p *coroP, main *coroG) bool {
	for {
		g, ok := coro.NextRunnable(p)
		if !ok {
			return false
		}
		if g == nil {
			// Platform event-loop integration is the next adapter layer. Never
			// confuse an empty ready queue with completion while parked Gs remain.
			return !coro.HasWaiting(p)
		}
		if !coroRunG(p, g) {
			return false
		}
		if g == main && coro.DeadG(main) {
			// Command main must not drain background goroutines after returning.
			// Until the runtime can cancel every ready/suspended child safely, only
			// a fully terminal P is a supported main-return state.
			return coro.TerminalG(p, main)
		}
	}
}

// coroRunActions is deliberately a static dispatcher. The compiler-owned
// wrappers stay direct calls so scheduler internals do not introduce function
// values, interface dispatch, or unnecessary dual sync/async versions.
func coroRunActions(p *coroP, g *coroG, action coro.Action) bool {
	for {
		var ok bool
		switch action.Kind {
		case coro.ActionComplete:
			return coroReleaseCompletedTask(g)
		case coro.ActionYield, coro.ActionPark:
			return true
		case coro.ActionCheckResume, coro.ActionCheckDestroy:
			action, ok = coro.Checked(p, g, action, coroHandleDone(action.Handle))
		case coro.ActionResume:
			coroHandleResume(action.Handle)
			action, ok = coro.Resumed(p, g, action)
		case coro.ActionDestroy:
			coroHandleDestroy(action.Handle)
			for {
				next, committed := coro.Destroyed(p, g, action)
				if committed {
					action, ok = next, true
					break
				}
				if !coro.AcknowledgeTerminalSchedule(p, g, action) {
					ok = false
					break
				}
				// Retry only the scheduler commit. The LLVM handle was already
				// destroyed exactly once before entering this loop.
			}
		default:
			return false
		}
		if !ok {
			return false
		}
	}
}
