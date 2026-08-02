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

// This fixed workload keeps the portable scheduler surface independent of
// process arguments and filesystem initialization. It exercises goroutine,
// channel, two-event select, timer cancellation, and timer wake through only
// standard Go APIs, and can therefore run unchanged as a WASI command.
package main

import (
	"sync"
	"time"
)

func main() {
	values := make(chan int)
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		values <- 42
		done.Done()
	}()

	timeout := time.NewTimer(time.Second)
	select {
	case value := <-values:
		if value != 42 {
			panic("wrong channel value")
		}
	case <-timeout.C:
		panic("unexpected timeout")
	}
	if !timeout.Stop() {
		panic("timer already fired")
	}
	done.Wait()

	started := time.Now()
	time.Sleep(time.Millisecond)
	if time.Since(started) < time.Millisecond {
		panic("timer returned early")
	}
	println("ok wasm-core")
}
