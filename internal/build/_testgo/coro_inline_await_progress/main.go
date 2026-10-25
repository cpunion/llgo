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
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var never = make(chan struct{})

//go:noinline
func child(value uint64, blocked bool) uint64 {
	if blocked {
		<-never
	}
	return value*1664525 + 1013904223
}

//go:noinline
func nested(value uint64, blocked bool) uint64 {
	return child(value^0x9e37, blocked)
}

func main() {
	if len(os.Args) != 2 || os.Args[1] != "direct" && os.Args[1] != "nested" {
		panic("expected direct or nested progress mode")
	}
	mode := os.Args[1]
	isNested := mode == "nested"
	// Keep a genuinely blocking alternative in the callee's compilation plan.
	// The acceptance runner selects its synchronous return at runtime.
	blocked := os.Getenv("LLGO_TEST_BLOCK_PROGRESS_CALL") != ""
	start := time.Now()
	var completed atomic.Uint32
	go func() { completed.Store(1) }()
	value, calls := uint64(1), uint64(0)
	for completed.Load() == 0 {
		if !isNested {
			value = child(value, blocked)
		} else {
			value = nested(value, blocked)
		}
		calls++
		// Allow many complete service budgets before inspecting wall time, so
		// an OS pause at the first checkpoint cannot cause a false failure.
		if calls >= 65536 && calls%4096 == 0 && time.Since(start) > time.Second && completed.Load() == 0 {
			fmt.Println("inline await progress STARVED", mode, calls, value)
			os.Exit(2)
		}
	}
	fmt.Println("inline await progress ok", mode, calls, value)
}
