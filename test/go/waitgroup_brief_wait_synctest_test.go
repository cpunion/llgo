//go:build go1.25

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitGroupBriefWaitInBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var group sync.WaitGroup
		value := 0
		group.Go(func() {
			time.Sleep(time.Second)
			value = 42
		})
		group.Wait()
		if value != 42 {
			t.Fatal("durable Wait returned before completion")
		}
		synctest.Wait()
		// An emptied bubbled WaitGroup must retain the original disassociation
		// behavior when it is reused for another round.
		group.Go(func() { value = 84 })
		group.Wait()
		if value != 84 {
			t.Fatal("reused bubbled WaitGroup lost its completion")
		}
	})
}
