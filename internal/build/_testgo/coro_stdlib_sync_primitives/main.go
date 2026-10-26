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
	"sync/atomic"
)

var (
	mutex sync.Mutex
	cond  = sync.NewCond(&mutex)
	group sync.WaitGroup
	ready bool
)

const demandPageContenders = 600

func publish() {
	mutex.Lock()
	ready = true
	cond.Signal()
	mutex.Unlock()
	group.Done()
}

func awaitDemandGate(gate, ready, done *sync.WaitGroup) {
	ready.Done()
	gate.Wait()
	done.Done()
}

// All three Value writers use the stdlib first-publication pin region. Mixed
// contenders cover its failed-CAS retry as well as normal publication, while
// readers must never observe the transient type marker. In particular these
// operations must not acquire a native mutex on a worker and unlock it after
// resuming on an executor thread.
func atomicValueFirstPublication() {
	var readers sync.WaitGroup
	for round := 0; round < 8; round++ {
		readers.Add(12)
		for reader := 0; reader < 12; reader++ {
			go func() {
				var value atomic.Value
				var writers sync.WaitGroup
				writers.Add(3)
				for mode := 0; mode < 3; mode++ {
					go func() {
						switch mode {
						case 0:
							value.Store(7)
						case 1:
							value.Swap(7)
						case 2:
							value.CompareAndSwap(nil, 7)
						}
						writers.Done()
					}()
				}
				for read := 0; read < 1000; read++ {
					if got := value.Load(); got != nil && got != 7 {
						panic("atomic.Value exposed an incomplete publication")
					}
				}
				writers.Wait()
				if value.Load() != 7 {
					panic("atomic.Value lost its completed publication")
				}
				readers.Done()
			}()
		}
		readers.Wait()
	}
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

	// More than eight inline 64-slot Manual sources can represent in aggregate.
	// Independent gates exercise concurrent standard semaphore waits without
	// turning this correctness gate into hundreds of serialized Mutex handoffs.
	demandGates := make([]sync.WaitGroup, demandPageContenders)
	var demandReady, demandDone sync.WaitGroup
	demandReady.Add(demandPageContenders)
	demandDone.Add(demandPageContenders)
	for index := range demandGates {
		demandGates[index].Add(1)
		go awaitDemandGate(&demandGates[index], &demandReady, &demandDone)
	}
	demandReady.Wait()
	for index := range demandGates {
		demandGates[index].Done()
	}
	demandDone.Wait()
	for _, procs := range []int{1, 4} {
		runtime.GOMAXPROCS(procs)
		atomicInstructionStress()
	}
	for _, procs := range []int{1, 2} {
		runtime.GOMAXPROCS(procs)
		atomicValueFirstPublication()
	}
	runtime.GOMAXPROCS(previous)
}

// atomicInstructionStress exercises the SC read/modify/write instructions
// selected by both the generic and LSE ARM64 backends. Distinct worker bits
// make a lost bit update observable without requiring one timing schedule.
func atomicInstructionStress() {
	const workers, rounds = 16, 1024
	var count32 atomic.Uint32
	var count64 atomic.Uint64
	var bits32 atomic.Uint32
	var bits64 atomic.Uint64
	var swap32 atomic.Uint32
	var swap64 atomic.Uint64
	var done sync.WaitGroup
	done.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			mask32, mask64 := uint32(1)<<worker, uint64(1)<<worker
			for repeat := 0; repeat < rounds; repeat++ {
				count32.Add(1)
				for {
					old := count32.Load()
					if count32.CompareAndSwap(old, old+1) {
						break
					}
					runtime.Gosched()
				}
				count64.Add(1)
				for {
					old := count64.Load()
					if count64.CompareAndSwap(old, old+1) {
						break
					}
					runtime.Gosched()
				}
				bits32.Or(mask32)
				bits64.Or(mask64)
				if bits32.Load()&mask32 == 0 || bits64.Load()&mask64 == 0 {
					panic("atomic bit update lost an owned bit")
				}
				bits32.And(^mask32)
				bits64.And(^mask64)
				if swap32.Swap(uint32(worker+1)) > workers || swap64.Swap(uint64(worker+1)) > workers {
					panic("atomic exchange returned a torn value")
				}
			}
			done.Done()
		}()
	}
	done.Wait()
	if count32.Load() != workers*rounds*2 || count64.Load() != workers*rounds*2 || bits32.Load() != 0 || bits64.Load() != 0 {
		panic("atomic RMW final value mismatch")
	}

	// A ready flag publishes freshly initialized ordinary fields through SC
	// stores/loads. The acknowledgement prevents the next publication from
	// overtaking consumption of the current record.
	type packet struct{ sequence, inverse uint64 }
	var pointer atomic.Pointer[packet]
	var ready atomic.Uint32
	done.Add(1)
	go func() {
		for sequence := uint64(1); sequence <= 512; sequence++ {
			for ready.Load() != 0 {
				runtime.Gosched()
			}
			pointer.Store(&packet{sequence, ^sequence})
			ready.Store(1)
		}
		done.Done()
	}()
	for sequence := uint64(1); sequence <= 512; sequence++ {
		for ready.Load() == 0 {
			runtime.Gosched()
		}
		value := pointer.Load()
		if value == nil || value.sequence != sequence || value.inverse != ^sequence {
			panic("atomic pointer exposed an incomplete publication")
		}
		ready.Store(0)
	}
	done.Wait()
}
