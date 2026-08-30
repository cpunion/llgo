//go:build llgo && !windows && !nogc && !baremetal && !wasm && !tinygo.wasm && (darwin || linux)

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
	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
	"github.com/xgo-dev/llgo/runtime/internal/thread"
)

func gLifecycleDestructor(_ thread.KeyDestructor) thread.KeyDestructor {
	return destroyUnixG
}

func destroyUnixG(ptr c.Pointer) {
	gp := (*g)(ptr)
	registered := gp != nil &&
		gp.coroState&gForeignThreadGCRegistrationOwnedFlag != 0
	if registered {
		gp.coroState &^= gForeignThreadGCRegistrationOwnedFlag
	}
	destroyG(ptr)
	if registered {
		bdwgc.UnregisterMyThread()
	}
}

// EnterForeignThread registers a pthread created outside LLGo before it first
// allocates a physical runtime placeholder or exposes its stack to BDWGC. The
// registration is retained by the existing pthread-key lifetime rather than
// repeated for every exported Go call on the same host thread.
func EnterForeignThread() bool {
	if getgIfPresent() != nil || bdwgc.ThreadIsRegistered() != 0 {
		return false
	}
	var base bdwgc.StackBase
	if bdwgc.GetStackBase(&base) != bdwgc.Success {
		panic("runtime: failed to discover foreign thread stack")
	}
	switch status := bdwgc.RegisterMyThread(&base); status {
	case bdwgc.Success:
		gp := getg()
		if gp == nil || gp.coroState&gForeignThreadGCRegistrationOwnedFlag != 0 {
			bdwgc.UnregisterMyThread()
			return false
		}
		gp.coroState |= gForeignThreadGCRegistrationOwnedFlag
		return true
	case bdwgc.Duplicate:
		return false
	default:
		panic("runtime: failed to register foreign thread")
	}
}

func ExitForeignThread(registered bool) {
	if registered && getgIfPresent() == nil {
		bdwgc.UnregisterMyThread()
	}
}
