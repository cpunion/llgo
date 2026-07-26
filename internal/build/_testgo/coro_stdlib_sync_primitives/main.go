/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package main

import (
	"runtime"
	"runtime/debug"
	"sync"
)

var (
	mutex sync.Mutex
	cond  = sync.NewCond(&mutex)
	group sync.WaitGroup
	ready bool
)

func publish() {
	mutex.Lock()
	ready = true
	cond.Signal()
	mutex.Unlock()
	group.Done()
}

func main() {
	previous := runtime.GOMAXPROCS(1)
	if previous < 1 || runtime.GOMAXPROCS(0) != 1 {
		panic("runtime.GOMAXPROCS shrink/query failed")
	}
	if runtime.GOMAXPROCS(previous) != 1 || runtime.GOMAXPROCS(0) != previous {
		panic("runtime.GOMAXPROCS restore/query failed")
	}
	runtime.SetDefaultGOMAXPROCS()
	if runtime.GOMAXPROCS(0) < 1 {
		panic("runtime.SetDefaultGOMAXPROCS produced an invalid limit")
	}
	previousThreads := debug.SetMaxThreads(64)
	if previousThreads != 10_000 || debug.SetMaxThreads(previousThreads) != 64 {
		panic("runtime/debug.SetMaxThreads query/restore failed")
	}

	group.Add(1)
	mutex.Lock()
	go publish()
	for !ready {
		cond.Wait()
	}
	mutex.Unlock()
	group.Wait()
}
