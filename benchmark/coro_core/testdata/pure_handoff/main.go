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

// This fixture deliberately has no imports or output. It isolates one child
// goroutine and two unbuffered channels from standard-library, timer, poller,
// and worker-executor costs. Each iteration is one request/ack round trip.
package main

const handoffs = 100_000

var result int

func main() {
	values := make(chan int)
	acks := make(chan int)
	done := make(chan struct{})
	go func() {
		for range handoffs {
			acks <- <-values + 1
		}
		close(done)
	}()

	checksum := 0
	for value := range handoffs {
		values <- value
		checksum ^= <-acks
	}
	<-done
	result = checksum
}
