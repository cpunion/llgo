/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package main

import "sync"

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
	group.Add(1)
	mutex.Lock()
	go publish()
	for !ready {
		cond.Wait()
	}
	mutex.Unlock()
	group.Wait()
}
