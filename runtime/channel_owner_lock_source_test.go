//go:build !llgo

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

package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestCoroChannelStateUsesNonblockingOwnerGate(t *testing.T) {
	channelSource, err := os.ReadFile("internal/runtime/z_chan.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(channelSource), "mutex channelMutex") {
		t.Fatal("Chan does not select the coroutine-aware channel state gate")
	}

	ownerPath := "internal/runtime/z_chan_lock_coro.go"
	ownerSource, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	ownerText := string(ownerSource)
	for _, marker := range []string{
		"//go:build (llgo && llgo_coro) || coro_channel_owner_test",
		"one CAS",
		"must never be held across llvm.coro.suspend",
		"channelMutexCompareAndSwap(&m.state, channelOwnerGateIdle, channelOwnerGateHeld)",
		"channelMutexCompareAndSwap(&m.state, channelOwnerGateHeld, channelOwnerGateIdle)",
		"contended or reentrant coroutine channel owner gate",
	} {
		if !strings.Contains(ownerText, marker) {
			t.Errorf("%s lacks owner-gate contract %q", ownerPath, marker)
		}
	}
	for _, forbidden := range []string{"pthread", "schedulerwait", "time.Sleep", "coroSchedulerYield"} {
		if strings.Contains(ownerText, forbidden) {
			t.Errorf("%s contains blocking/foreign owner-gate dependency %q", ownerPath, forbidden)
		}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), ownerPath, ownerSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			t.Errorf("%s contains a loop in the owner gate", ownerPath)
		}
		return true
	})

	atomicSource, err := os.ReadFile("internal/runtime/z_chan_lock_coro_atomic_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	atomicText := string(atomicSource)
	if !strings.Contains(atomicText, "catomic.CompareAndExchange(address, old, new)") {
		t.Error("llgo_coro channel owner gate is not target-lowered atomic CAS")
	}
	if strings.Contains(atomicText, "pthread") {
		t.Error("llgo_coro channel owner gate atomic backend reaches pthread")
	}

	fallback, err := os.ReadFile("internal/runtime/z_chan_lock_pthread.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fallback), "type channelMutex = sync.Mutex") {
		t.Error("non-coro channel implementation lost its pthread-compatible fallback")
	}
}
