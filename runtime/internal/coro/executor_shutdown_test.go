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

import "testing"

func TestExecutorShutdownDrainEmptyDomainIsCloseReady(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || needed {
		t.Fatalf("empty executor shutdown drain = (%t,%t)", needed, ok)
	}
	if !ExecutorShutdownDrained(driver) {
		t.Fatal("empty executor domain was not close-ready")
	}
}

func TestExecutorShutdownDrainRequestsEveryReadyTask(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	first := newYieldingTestG(t, "executor-shutdown-first")
	second := newYieldingTestG(t, "executor-shutdown-second")
	if !Enqueue(p, first.g) || !Enqueue(p, second.g) {
		t.Fatal("enqueue executor shutdown tasks")
	}
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || !needed {
		t.Fatalf("executor shutdown drain = (%t,%t)", needed, ok)
	}
	for _, task := range []*yieldingTestG{first, second} {
		if kind, present := TaskCancellationOf(p, task.g); !present || kind != TaskCancelShutdown {
			t.Fatalf("task %s cancellation = (%d,%t)", task.name, kind, present)
		}
	}
	if ExecutorShutdownDrained(driver) {
		t.Fatal("executor became close-ready before canceled tasks drained")
	}
	late := newYieldingTestG(t, "executor-shutdown-late-spawn")
	if !Enqueue(p, late.g) {
		t.Fatal("enqueue task created during shutdown cleanup")
	}
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || !needed {
		t.Fatalf("repeat executor shutdown drain = (%t,%t)", needed, ok)
	}
	if kind, present := TaskCancellationOf(p, late.g); !present || kind != TaskCancelShutdown {
		t.Fatalf("late task cancellation = (%d,%t)", kind, present)
	}
}
