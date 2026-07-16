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

package coro

import (
	"runtime"
	"sync"
	"testing"
)

func TestTargetIngressStrongSealJoinsWholeShim(t *testing.T) {
	var ingress TargetIngress
	if !ingress.CanReleaseResources() || !ingress.Start() || ingress.CanReleaseResources() {
		t.Fatal("start target ingress")
	}

	const producers = 32
	entered := make(chan struct{}, producers)
	release := make(chan struct{})
	results := make(chan [2]bool, producers)
	var wg sync.WaitGroup
	for producer := 0; producer < producers; producer++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !ingress.Enter() {
				results <- [2]bool{false, false}
				return
			}
			entered <- struct{}{}
			<-release
			sealed, ok := ingress.Leave()
			results <- [2]bool{sealed, ok}
		}()
	}
	for producer := 0; producer < producers; producer++ {
		<-entered
	}

	if !ingress.Seal() || ingress.Enter() || ingress.Quiesced() || ingress.Retire() {
		t.Fatal("seal admitted a new producer or retired before join")
	}
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if result != [2]bool{true, true} {
			t.Fatalf("sealed producer release = %v", result)
		}
	}
	if !ingress.Quiesced() || !ingress.Retire() || !ingress.CanReleaseResources() || ingress.Enter() || ingress.Start() {
		t.Fatal("strongly joined ingress did not retire permanently")
	}
}

func TestTargetIngressEnterSealRace(t *testing.T) {
	for iteration := 0; iteration < 1000; iteration++ {
		var ingress TargetIngress
		if !ingress.Start() {
			t.Fatal("start race ingress")
		}
		start := make(chan struct{})
		entered := make(chan bool, 1)
		go func() {
			<-start
			ok := ingress.Enter()
			entered <- ok
			if ok {
				_, _ = ingress.Leave()
			}
		}()
		close(start)
		runtime.Gosched()
		if !ingress.Seal() {
			t.Fatal("seal race ingress")
		}
		if <-entered {
			for !ingress.Quiesced() {
				runtime.Gosched()
			}
		} else if !ingress.Quiesced() {
			t.Fatal("rejected enter left an inflight lease")
		}
		if !ingress.Retire() {
			t.Fatal("retire raced ingress")
		}
	}
}
