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
	"testing"
)

type currentExecutorDriverFixture struct {
	p      *P
	driver *ExecutorDriver
	handle ExecutorHandle
	route  RouteID
	task   *yieldingTestG
	action Action
}

func bindCurrentExecutorDriverFixture(
	t *testing.T,
	registry *ExecutorRegistry,
	route RouteID,
	name string,
) *currentExecutorDriverFixture {
	t.Helper()
	fixture := &currentExecutorDriverFixture{
		p:      new(P),
		driver: new(ExecutorDriver),
		route:  route,
		task:   newYieldingTestG(t, name),
	}
	fixture.handle = registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalogAtRoute(
		fixture.driver, fixture.p, registry, fixture.handle, route, ExecutorSourceCatalog{},
	) {
		t.Fatalf("bind current-executor fixture %q at route %d", name, route)
	}
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatalf("enqueue current-executor fixture %q", name)
	}
	if next, ok := NextRunnable(fixture.p); !ok || next != fixture.task.g {
		t.Fatalf("dequeue current-executor fixture %q = (%p, %t)", name, next, ok)
	}
	return fixture
}

func requireCurrentExecutorDriver(
	t *testing.T,
	g *G,
	wantDriver *ExecutorDriver,
	wantHandle ExecutorHandle,
	wantRoute RouteID,
) {
	t.Helper()
	driver, handle, route, ok := CurrentExecutorDriver(g)
	if !ok || driver != wantDriver || handle != wantHandle || route != wantRoute {
		t.Fatalf("current executor = (%p, %+v, %d, %t), want (%p, %+v, %d, true)",
			driver, handle, route, ok, wantDriver, wantHandle, wantRoute)
	}
}

func requireNoCurrentExecutorDriver(t *testing.T, g *G) {
	t.Helper()
	if driver, handle, route, ok := CurrentExecutorDriver(g); ok || driver != nil ||
		handle != (ExecutorHandle{}) || route != 0 {
		t.Fatalf("invalid current executor resolved as (%p, %+v, %d, %t)", driver, handle, route, ok)
	}
}

func TestCurrentExecutorDriverResolvesExactOwnerAcrossTwoP(t *testing.T) {
	registry := new(ExecutorRegistry)
	first := bindCurrentExecutorDriverFixture(t, registry, RouteID(3), "current-owner-first")
	second := bindCurrentExecutorDriverFixture(t, registry, RouteID(7), "current-owner-second")

	// Merely entering llvm.coro.resume is not sufficient. Compiler-generated
	// code must consume the exact run decision before calling an owner hook.
	first.action = beginWaitTestResumeWithoutGate(t, first.p, first.task)
	requireNoCurrentExecutorDriver(t, first.task.g)
	takeNormalResumeGateForTest(t, first.task.g)
	second.action = beginWaitTestResume(t, second.p, second.task)

	requireCurrentExecutorDriver(t, first.task.g, first.driver, first.handle, first.route)
	requireCurrentExecutorDriver(t, second.task.g, second.driver, second.handle, second.route)
	if first.handle == second.handle || first.route == second.route {
		t.Fatal("two-P fixture did not allocate distinct executor identities")
	}

	var idle G
	if !InitG(&idle) {
		t.Fatal("initialize idle wrong-G fixture")
	}
	requireNoCurrentExecutorDriver(t, nil)
	requireNoCurrentExecutorDriver(t, &G{})
	requireNoCurrentExecutorDriver(t, &idle)

	t.Run("wrong run P", func(t *testing.T) {
		saved := first.task.g.runP
		first.task.g.runP = second.p
		defer func() { first.task.g.runP = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	t.Run("wrong current G", func(t *testing.T) {
		saved := first.p.current
		first.p.current = second.task.g
		defer func() { first.p.current = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	t.Run("wrong executor pointer", func(t *testing.T) {
		saved := first.p.executor
		first.p.executor = second.driver
		defer func() { first.p.executor = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	t.Run("mismatched route", func(t *testing.T) {
		saved := first.driver.route
		first.driver.route = second.route
		defer func() { first.driver.route = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	t.Run("stale executor generation", func(t *testing.T) {
		saved := first.driver.handle
		first.driver.handle.Generation++
		defer func() { first.driver.handle = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	t.Run("runnable transfer generation", func(t *testing.T) {
		saved := first.task.g.transferState
		first.task.g.transferState = runnableTransferGPublished
		defer func() { first.task.g.transferState = saved }()
		requireNoCurrentExecutorDriver(t, first.task.g)
	})

	yieldRunningDriverTask(t, first.p, first.task, first.action)
	requireNoCurrentExecutorDriver(t, first.task.g)
	yieldRunningDriverTask(t, second.p, second.task, second.action)
	requireNoCurrentExecutorDriver(t, second.task.g)

	closeTestExecutorDriver(t, first.driver)
	closeTestExecutorDriver(t, second.driver)
	finishReadyDriverTasks(t, first.p, map[*G]*yieldingTestG{first.task.g: first.task})
	finishReadyDriverTasks(t, second.p, map[*G]*yieldingTestG{second.task.g: second.task})
	if !TerminalG(first.p, first.task.g) || !TerminalG(second.p, second.task.g) ||
		!registry.CanRelease() {
		t.Fatal("two-P current-executor fixture retained terminal state")
	}
	runtime.KeepAlive(first.task.frame.memory)
	runtime.KeepAlive(second.task.frame.memory)
}
