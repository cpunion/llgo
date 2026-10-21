//go:build go1.25

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"testing"
	"testing/synctest"
)

func TestLocalSingleSlotChannelInBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		values := make(chan uint32, 1)
		values <- 0xfedcba98
		if got := <-values; got != 0xfedcba98 {
			t.Fatalf("bubbled channel value = %#x", got)
		}
		close(values)
		if value, ok := <-values; value != 0 || ok {
			t.Fatalf("closed bubbled receive = %#x, %t", value, ok)
		}
	})
}
