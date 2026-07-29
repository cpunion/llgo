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
		c.Fprintf(c.Stderr, c.Str("runtime: pthread_key_create failed (errno=%d)\n"), ret)
		return false
	}
	gKey = key
	gKeyReady = true
	return true
}

func getg() *g {
	if ptr := gKey.Get(); ptr != nil {
		return (*g)(ptr)
	}
	gp := initRuntimeContext(allocRuntimeContext(), nil, _Grunning)
	if ret := setgRaw(gp); ret != 0 {
		destroyG(c.Pointer(unsafe.Pointer(gp)))
		c.Fprintf(c.Stderr, c.Str("runtime: pthread_setspecific failed (errno=%d)\n"), ret)
		coroRuntimeAbort("failed to install runtime g")
		return nil
	}
	return gp
}

func setg(gp *g) {
	if ret := setgRaw(gp); ret != 0 {
		c.Fprintf(c.Stderr, c.Str("runtime: pthread_setspecific failed (errno=%d)\n"), ret)
		coroRuntimeAbort("failed to install runtime g")
	}
}

func setgRaw(gp *g) c.Int {
	return gKey.Set(c.Pointer(unsafe.Pointer(gp)))
}

func destroyG(ptr c.Pointer) {
	gp := (*g)(ptr)
	if gp == nil {
		return
	}
	if gp.panic_ != nil {
		c.Free(gp.panic_)
	}
	ctx := gp.context
	if ctx != nil && ctx.root != nil {
		root := ctx.root
		ctx.root = nil
		FreeRoot(root)
	}
}
