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

package main

import (
	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const LLGoFiles = "wrap/hold.c"

//llgo:coro noblock
//go:linkname hold C.llgo_deferred_demand_hold
func hold(unsafe.Pointer)
func main() {
	runtime.GOMAXPROCS(1)
	f, e := os.CreateTemp("", "llgo-deferred-demand-*")
	if e != nil {
		panic(e)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	words := new([4]uint32)
	done := make(chan struct{}, 1)
	go func() {
		for atomic.LoadUint32(&words[2]) == 0 {
			runtime.Gosched()
		}
		hold(unsafe.Pointer(words))
		done <- struct{}{}
	}()
	corort.DeferredDemandProbeEnable(unsafe.Pointer(words))
	if _, e := syscall.Seek(int(f.Fd()), 0, 0); e != nil {
		panic(e)
	}
	<-done
	if atomic.LoadUint32(&words[3]) != 1 {
		panic("post-arm demand read rejected active replacement")
	}
	println("ok deferred demand publication")
}
