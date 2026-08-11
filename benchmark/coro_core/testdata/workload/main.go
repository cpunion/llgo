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

// This program deliberately uses only ordinary Go APIs. The same source is
// compiled by the main and coroutine backends so the comparison does not
// depend on an LLGo-only observation hook or benchmark implementation.
package main

import (
	"os"
	"sync"
	"time"
)

const (
	maximumWork        = 100_000_000
	maximumConcurrency = 10_000
)

//go:noinline
func arithmeticStep(value, salt int) int {
	value ^= salt + 0x1f123bb5
	return (value*1_664_525 + 1_013_904_223) & 0x7fff_ffff
}

func parsePositive(text string, allowZero bool) (int, bool) {
	if text == "" {
		return 0, false
	}
	value := 0
	for _, char := range text {
		if char < '0' || char > '9' || value > (maximumWork-int(char-'0'))/10 {
			return 0, false
		}
		value = value*10 + int(char-'0')
	}
	return value, allowZero || value != 0
}

func spawn(count, rounds int) int {
	for range rounds {
		var done sync.WaitGroup
		done.Add(count)
		for range count {
			go done.Done()
		}
		done.Wait()
	}
	return count * rounds
}

func compute(count, rounds int) int {
	value := 1
	for round := range rounds {
		for index := range count {
			value = arithmeticStep(value, round*count+index)
		}
	}
	return value
}

func parallelCompute(count, rounds int) int {
	results := make(chan int, count)
	for worker := range count {
		go func() {
			value := worker + 1
			for index := range rounds {
				value = arithmeticStep(value, worker*rounds+index)
			}
			results <- value
		}()
	}
	checksum := 0
	for range count {
		checksum ^= <-results
	}
	return checksum
}

func buffered(count, rounds int) int {
	values := make(chan int, 1)
	checksum := 0
	for round := range rounds {
		for index := range count {
			value := round*count + index
			values <- value
			checksum ^= <-values
		}
	}
	return checksum
}

func selectReady(count, rounds int) int {
	left := make(chan int, 1)
	right := make(chan int, 1)
	checksum := 0
	for round := range rounds {
		for index := range count {
			value := round*count + index
			left <- value
			right <- value + 1
			select {
			case selected := <-left:
				checksum ^= selected
				checksum ^= <-right
			case selected := <-right:
				checksum ^= selected
				checksum ^= <-left
			}
		}
	}
	return checksum
}

func park(count int) int {
	var ready, done sync.WaitGroup
	ready.Add(count)
	done.Add(count)
	release := make(chan struct{})
	for range count {
		go func() {
			ready.Done()
			<-release
			done.Done()
		}()
	}
	ready.Wait()
	close(release)
	done.Wait()
	return count
}

func handoff(count, rounds int) int {
	values := make(chan int)
	acks := make(chan int)
	done := make(chan struct{})
	go func() {
		for value := range values {
			acks <- value + 1
		}
		close(done)
	}()
	checksum := 0
	for round := range rounds {
		for index := range count {
			value := round*count + index
			values <- value
			checksum ^= <-acks
		}
	}
	close(values)
	<-done
	return checksum
}

func timers(count, rounds int) int {
	completed := 0
	for range rounds {
		var ready, done sync.WaitGroup
		ready.Add(count)
		done.Add(count)
		start := make(chan struct{})
		for range count {
			go func() {
				ready.Done()
				<-start
				time.Sleep(time.Millisecond)
				done.Done()
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		completed += count
	}
	return completed
}

func main() {
	mode := "idle"
	count := 0
	rounds := 1
	if len(os.Args) != 1 {
		if len(os.Args) != 4 {
			panic("usage: workload <idle|compute|parallel|buffered|select|spawn|park|handoff|timers> <count> <rounds>")
		}
		var ok bool
		mode = os.Args[1]
		if count, ok = parsePositive(os.Args[2], mode == "idle" || mode == "park"); !ok {
			panic("invalid count")
		}
		if rounds, ok = parsePositive(os.Args[3], false); !ok || count != 0 && rounds > maximumWork/count {
			panic("invalid rounds")
		}
		if count > maximumConcurrency &&
			(mode == "parallel" || mode == "spawn" || mode == "park" || mode == "timers") {
			panic("concurrency limit exceeded")
		}
	}

	started := time.Now()
	result := 0
	switch mode {
	case "idle":
		if count != 0 || rounds != 1 {
			panic("idle requires count 0 and rounds 1")
		}
		result = 1
	case "compute":
		result = compute(count, rounds)
	case "parallel":
		result = parallelCompute(count, rounds)
	case "buffered":
		result = buffered(count, rounds)
	case "select":
		result = selectReady(count, rounds)
	case "spawn":
		result = spawn(count, rounds)
	case "park":
		if rounds != 1 {
			panic("park requires one round")
		}
		result = park(count)
	case "handoff":
		result = handoff(count, rounds)
	case "timers":
		result = timers(count, rounds)
	default:
		panic("unknown mode")
	}
	elapsed := time.Since(started)
	println("ok", mode, count, rounds, result, elapsed.Nanoseconds())
}
