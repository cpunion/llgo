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
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

const count = 8
const LLGoFiles = "$(pkg-config --cflags bdw-gc): wrap/retain.c"

//go:linkname startCollector C.llgo_worker_mapping_collector_start
func startCollector(gate *uint32) uintptr

//go:linkname holdBuffer C.llgo_worker_mapping_hold
func holdBuffer(pointer unsafe.Pointer, size, index uintptr) uintptr

//go:linkname joinCollector C.llgo_worker_mapping_collector_join
func joinCollector() uintptr

// Four calls occupy native workers while additional callers prepare distinct
// buffers. Compiler frames must retain their arguments across forced GC.
func main() {
	var gate uint32
	if startCollector(&gate) != 0 {
		panic("cannot start collector")
	}
	var ready, done sync.WaitGroup
	ready.Add(count)
	done.Add(count)
	for index := 0; index < count; index++ {
		go func(index int) {
			data := make([]byte, 64*1024)
			for offset := range data {
				data[offset] = byte(index*17 + offset)
			}
			ready.Done()
			if holdBuffer(unsafe.Pointer(&data[0]), uintptr(len(data)), uintptr(index)) != 0 {
				panic("worker buffer lost during collection")
			}
			done.Done()
		}(index)
	}
	ready.Wait()
	atomic.StoreUint32(&gate, 1)
	done.Wait()
	if joinCollector() != 0 {
		panic("collector did not finish four full collections")
	}
	runtime.KeepAlive(&gate)
	println("worker mapping retention ok")
}
