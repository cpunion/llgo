//go:build (baremetal && !wasm) || (!baremetal && (wasm || tinygo.wasm))

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

import rtdebug "github.com/goplus/llgo/runtime/internal/runtime"

// A stackless target has no physical frame-liveness marker. Visibility of a
// recovered panic snapshot is instead owned by the scheduler's exact
// CompletionRecord ancestry.
func panicSplicePCs() []uintptr {
	pcs := rtdebug.PanicPCs()
	if len(pcs) != 0 && (rtdebug.PanicActive() || rtdebug.CoroPanicRecoverActive()) {
		return pcs
	}
	return nil
}

func stacklessPanicFrame(pc uintptr) (rtdebug.CallerFrame, bool) {
	if frame, ok := rtdebug.FrameForPC(pc); ok {
		return frame, true
	}
	if pc != 0 {
		return rtdebug.FrameForPC(pc - 1)
	}
	return rtdebug.CallerFrame{}, false
}

// copyPanicSplicedCallers joins two compiler-maintained logical stacks. It
// intentionally does not consult dladdr, a frame pointer, or native text
// bounds, none of which are part of the stackless target contract.
func copyPanicSplicedCallers(cur []uintptr, skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	view := cur
	snap := panicSplicePCs()
	if len(snap) != 0 {
		for i := 0; i < len(cur); i++ {
			live, liveOK := stacklessPanicFrame(cur[i])
			if !liveOK {
				continue
			}
			for j := 0; j < len(snap); j++ {
				saved, savedOK := stacklessPanicFrame(snap[j])
				if !savedOK {
					continue
				}
				sameEntry := live.Entry != 0 && saved.Entry == live.Entry
				sameFunction := live.Function != "" && saved.Function == live.Function
				sameSource := live.File != "" && saved.File == live.File &&
					live.Line > 0 && saved.Line == live.Line
				if sameEntry || sameFunction || sameSource {
					joined := make([]uintptr, 0, i+len(snap))
					joined = append(joined, cur[:i]...)
					joined = append(joined, snap...)
					view = joined
					i = len(cur)
					break
				}
			}
		}
	}
	if skip < 0 {
		skip = 0
	}
	if skip >= len(view) {
		return 0
	}
	return copy(pc, view[skip:])
}
