//go:build !baremetal

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

// This bounded program deliberately uses only ordinary Go APIs so Go gc and
// the coroutine backend execute the same progress contract.
package main

import (
	"time"
)

const spinLimit = 100_000_000

//go:noinline
func spinStep(value, salt uint32) uint32 {
	return (value^(salt+0x1f123bb5))*1_664_525 + 1_013_904_223
}

func main() {
	fired := make(chan struct{}, 1)
	go func() {
		time.Sleep(2 * time.Millisecond)
		fired <- struct{}{}
	}()

	value := uint32(1)
	for iteration := 0; iteration != spinLimit; iteration++ {
		value = spinStep(value, uint32(iteration))
	}
	select {
	case <-fired:
	default:
		panic("timer did not preempt the sole runnable compute loop")
	}
	println("ok preempt-timer", spinLimit, value)
}
