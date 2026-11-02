/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 * http://www.apache.org/licenses/LICENSE-2.0
 */

package coro

import (
	"runtime"
	"testing"
)

func TestOSThreadPhysicalReturnHandoffPreservesValidRunnerCursor(t *testing.T) {
	for _, withPeer := range []bool{false, true} {
		name := "return-without-local-peer"
		if withPeer {
			name = "return-with-local-peer"
		}
		t.Run(name, func(t *testing.T) {
			p := new(P)
			driver, _, _ := bindTestExecutorDriver(t, p)
			owner := newYieldingTestG(t, "return-debt-owner")
			peer := newYieldingTestG(t, "return-debt-peer")
			if !Enqueue(p, owner.g) || withPeer && !Enqueue(p, peer.g) {
				t.Fatal("enqueue physical-return fixture")
			}
			action := commitLockedRunnerYield(t, driver, owner)
			// A completed source or same-resume publication can owe the attached
			// owner one physical action. A foreign caller may then request return
			// before this owner resumes, changing which task this M may select.
			driver.run.readyDebt = true
			if _, ok := driver.Route(); !ok {
				t.Fatal("attached ready owner did not validate")
			}
			if required, ok := PrepareOSThreadSuspendHandoffForPhysicalReturn(
				driver, owner.g, action.Kind,
			); !required || !ok {
				t.Fatalf("prepare physical return = (%t, %t)", required, ok)
			}
			if _, ok := driver.Route(); !ok {
				t.Fatal("physical return invalidated driver route")
			}
			if _, ok := BeginExecutorRunSlice(driver); !ok {
				t.Fatal("physical return invalidated next bounded slice")
			}
			if detached, ready, ok := OSThreadSuspendHandoffStatus(driver); !ok || !detached || ready {
				t.Fatalf("physical return status = (%t, %t, %t)", detached, ready, ok)
			}
			if withPeer {
				step := runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
				runnerYieldAction(t, driver, step, peer)
				if detached, ready, ok := OSThreadSuspendHandoffStatus(driver); !ok || !detached || !ready {
					t.Fatalf("serviced physical return = (%t, %t, %t)", detached, ready, ok)
				}
			}
			runtime.KeepAlive(owner.frame.memory)
			runtime.KeepAlive(peer.frame.memory)
		})
	}
}
