//go:build llgo && !baremetal && !wasm && !tinygo.wasm

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

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

var (
	gKey      pthread.Key
	gKeyReady bool
)

// The pthread key remains the physical thread's lifetime owner and destructor
// registration. A stackless logical G, however, changes at every physical
// llvm.coro.resume. Mirroring that hot current value in native C TLS avoids a
// pthread_get/setspecific call around every resume without weakening thread
// teardown: the key continues to name the independently allocated physical
// placeholder for the complete lifetime of a coroutine executor M.

//go:linkname coroCurrentGLoadV1 C.__llgo_coro_current_g_load_v1
func coroCurrentGLoadV1() unsafe.Pointer

//go:linkname coroCurrentGStoreV1 C.__llgo_coro_current_g_store_v1
//llgo:coro noblock
func coroCurrentGStoreV1(unsafe.Pointer)

func init() {
	if !coroRuntimeContextBootstrap() {
		coroRuntimeAbort("failed to create getg key")
	}
}

// coroRuntimeContextBootstrap initializes the executor-thread getg slot before
// the compiler starts the coroutine scheduler. The ordinary runtime init calls
// it again idempotently; by then the first logical G may already have crossed
// coroEnterRuntimeContext and therefore cannot be the operation which creates
// this key.
func coroRuntimeContextBootstrap() bool {
	if gKeyReady {
		return true
	}
	var key pthread.Key
	if ret := key.Create(pthread.KeyDestructor(destroyG)); ret != 0 {
		// The caller owns the terminal diagnostic. Formatting through stdio here
		// would turn an unrecoverable pre-scheduler failure into an asynchronous
		// worker transaction even though there is no scheduler which can resume it.
		return false
	}
	gKey = key
	gKeyReady = true
	return true
}

func getg() *g {
	if ptr := coroCurrentGLoadV1(); ptr != nil {
		return (*g)(ptr)
	}
	// A thread which was initialized by an older/native entry path may have a
	// key owner before its direct TLS mirror is first observed. Populate the
	// mirror once; every subsequent lookup is the constant-time C TLS leaf.
	if ptr := gKey.Get(); ptr != nil {
		coroCurrentGStoreV1(unsafe.Pointer(ptr))
		return (*g)(ptr)
	}
	gp := initRuntimeContextUntracked(allocRuntimeContext(), nil, _Grunning)
	if ret := setgRaw(gp); ret != 0 {
		destroyG(c.Pointer(unsafe.Pointer(gp)))
		coroRuntimeAbort("failed to install runtime g")
		return nil
	}
	return gp
}

// getgIfPresent is the observation-only counterpart of getg. Runtime
// locality probes run inside an already installed managed resume and must not
// allocate or install a fallback context merely because no logical G is
// active. Keeping that cold initialization path out of this function also
// lets coroutine effect analysis preserve pthread TLS lookup as noblock.
func getgIfPresent() *g {
	return (*g)(coroCurrentGLoadV1())
}

func setg(gp *g) {
	if ret := setgRaw(gp); ret != 0 {
		coroRuntimeAbort("failed to install runtime g")
	}
}

func setgRaw(gp *g) c.Int {
	if ret := gKey.Set(c.Pointer(unsafe.Pointer(gp))); ret != 0 {
		return ret
	}
	coroCurrentGStoreV1(unsafe.Pointer(gp))
	return 0
}

// setgCoro switches only the logical G observed by code running inside one
// physical executor M. The pthread key deliberately remains bound to that M's
// placeholder so its destructor never takes ownership of an interior spawned-
// task context. coroEnter/LeaveRuntimeContext prove the balanced interval and
// restore the placeholder before the executor can return to its host boundary.
func setgCoro(gp *g) {
	coroCurrentGStoreV1(unsafe.Pointer(gp))
}

func destroyG(ptr c.Pointer) {
	gp := (*g)(ptr)
	if gp == nil {
		return
	}
	// A malformed executor which exits inside an unbalanced logical resume must
	// not free either the physical placeholder or an interior task context. The
	// normal owner protocol detects the missing resume/leave; this destructor is
	// only the final fail-safe against turning that violation into a use-after-
	// free while pthread teardown is unwinding.
	if current := (*g)(coroCurrentGLoadV1()); current != nil && current != gp {
		return
	}
	coroCurrentGStoreV1(nil)
	if gp.startarg != nil {
		// A managed logical G must restore the executor placeholder before
		// pthread exit. Do not mutate or free either a standalone command
		// context or an interior spawned-task context from the C destructor;
		// physical-owner shutdown detects the missing resume/leave separately.
		return
	}
	if gp.panic_ != nil {
		c.Free(gp.panic_)
		gp.panic_ = nil
	}
	releasePanicPCStore(gp)
	ctx := gp.context
	if ctx != nil {
		gp.context = nil
		FreeRoot(unsafe.Pointer(ctx))
	}
}
