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

type coroRunStopV1 uint8

const (
	coroRunInvalidV1 coroRunStopV1 = iota
	coroRunMainDoneV1
	coroRunExecutorSleepV1
	coroRunTerminalExecutorCloseV1
	coroRunPanicCompleteV1
)

type coroRunResultV1 struct {
	stop   coroRunStopV1
	g      *coroG
	action coro.Action
}

type coroActionStopV1 uint8

const (
	coroActionInvalidV1 coroActionStopV1 = iota
	coroActionSliceDoneV1
	coroActionTerminalExecutorCloseV1
	coroActionPanicCompleteV1
)

func coroInitG(g *coroG) bool {
	return coro.InitG(g)
}

func coroAdoptRoot(g *coroG, handle unsafe.Pointer) bool {
	return coro.AdoptRoot(g, handle)
}

func coroEnqueue(p *coroP, g *coroG) bool {
	return coro.Enqueue(p, g)
}

func coroRunG(p *coroP, g *coroG) (coroActionStopV1, coro.Action) {
	action, ok := coro.BeginRunG(p, g)
	if !ok {
		return coroActionInvalidV1, coro.Action{}
	}
	return coroRunActions(p, g, action)
}

func coroRun(p *coroP, main *coroG, driver *coro.ExecutorDriver) coroRunResultV1 {
	for {
		g, ok := coro.NextRunnable(p)
		if !ok {
			return coroRunResultV1{}
		}
		if g == nil {
			if !coro.HasWaiting(p) {
				return coroRunResultV1{}
			}
			sleep, prepared := coro.PrepareExecutorSleep(driver)
			if !prepared {
				return coroRunResultV1{}
			}
			if sleep {
				return coroRunResultV1{stop: coroRunExecutorSleepV1}
			}
			continue
		}
		stop, action := coroRunG(p, g)
		switch stop {
		case coroActionSliceDoneV1:
		case coroActionTerminalExecutorCloseV1:
			return coroRunResultV1{
				stop:   coroRunTerminalExecutorCloseV1,
				g:      g,
				action: action,
			}
		case coroActionPanicCompleteV1:
			return coroRunResultV1{
				stop:   coroRunPanicCompleteV1,
				g:      g,
				action: action,
			}
		default:
			return coroRunResultV1{}
		}
		if g == main && coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 && !coro.DeadG(main) {
			// The compiler hook is valid only on main's normal continuation
			// immediately before the bootstrap root's final suspend. Yielding or
			// parking after publishing the marker is an ABI violation.
			return coroRunResultV1{}
		}
		if g == main && coro.DeadG(main) {
			// Command main never drains background goroutines. The program adapter
			// either enters the explicit ready-child cancellation protocol after a
			// normal-main hook, or fails closed.
			return coroRunResultV1{stop: coroRunMainDoneV1, g: main}
		}
	}
}

// coroCancelReady destroys every ready child deepest-to-root. It deliberately
// never calls coro.done or coro.resume: command shutdown owns only suspended
// YieldOnly/AwaitStructured frame chains.
func coroCancelReady(p *coroP) bool {
	for {
		g, action, ok := coro.NextCommandCancel(p)
		if !ok {
			return false
		}
		if g == nil {
			return action.Kind == coro.ActionInvalid && action.Handle == nil
		}
		for {
			switch action.Kind {
			case coro.ActionCancelDestroy:
				coroHandleDestroy(action.Handle)
				action, ok = coro.CancelDestroyed(p, g, action)
				if !ok {
					return false
				}
			case coro.ActionCancelComplete:
				if action.Handle != nil || !coroReleaseCompletedTask(g) {
					return false
				}
				// g may have been physically freed. Never inspect it again.
				g = nil
				break
			default:
				return false
			}
			if g == nil {
				break
			}
		}
	}
}

// coroRunActions is deliberately a static dispatcher. The compiler-owned
// wrappers stay direct calls so scheduler internals do not introduce function
// values, interface dispatch, or unnecessary dual sync/async versions.
func coroRunActions(p *coroP, g *coroG, action coro.Action) (coroActionStopV1, coro.Action) {
	for {
		var ok bool
		switch action.Kind {
		case coro.ActionComplete:
			if !coroReleaseCompletedTask(g) {
				return coroActionInvalidV1, coro.Action{}
			}
			return coroActionSliceDoneV1, action
		case coro.ActionYield, coro.ActionPark:
			return coroActionSliceDoneV1, action
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
		case coro.ActionPanicDestroy:
			coroHandleDestroy(action.Handle)
			for {
				next, committed := coro.PanicDestroyed(p, g, action)
				if committed {
					action, ok = next, true
					break
				}
				if !coro.AcknowledgePanicTerminalSchedule(p, g, action) {
					ok = false
					break
				}
				// Retry only the state commit. The suspended ancestor handle was
				// already destroyed exactly once.
			}
		case coro.ActionPanicComplete:
			// The core has retained a stable task-local two-word record and has
			// destroyed every frame. Printing/fatal ownership and compiler-side
			// cleanup/recover semantics are not part of this prototype, so stop
			// here instead of misclassifying panic as ordinary G completion.
			if _, published := coro.LoadPanicRecord(g); !published {
				return coroActionInvalidV1, coro.Action{}
			}
			return coroActionPanicCompleteV1, action
		case coro.ActionTerminalExecutorClose:
			if action.Handle != nil {
				return coroActionInvalidV1, coro.Action{}
			}
			return coroActionTerminalExecutorCloseV1, action
		default:
			return coroActionInvalidV1, coro.Action{}
		}
		if !ok {
			return coroActionInvalidV1, coro.Action{}
		}
	}
}

// __llgo_coro_panic_prepare_v1 is the compiler-to-runtime terminal panic
// handoff. The physical G is an explicit ABI argument: this boundary must
// never discover scheduler ownership through TLS or a process-global current
// G. A rejected once-only publication is a terminal ABI violation and aborts
// immediately, so malformed cleanup/recover/Goexit/implicit-fault lowering
// cannot resume ordinary execution on a poisoned G.
//
//export __llgo_coro_panic_prepare_v1
func __llgo_coro_panic_prepare_v1(g, handle, header, typeWord, dataWord unsafe.Pointer) {
	if !coro.PreparePanic(
		(*coro.G)(g),
		handle,
		(*coro.HeaderV1)(header),
		typeWord,
		dataWord,
	) {
		coroRuntimeAbort("invalid coroutine panic handoff")
	}
}
