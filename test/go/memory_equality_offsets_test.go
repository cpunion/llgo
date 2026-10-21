// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import "testing"

func TestRegularMemoryEqualityUnalignedAndEveryByte(t *testing.T) {
	type packed struct {
		prefix byte
		bytes  [255]byte
	}
	var left, right packed
	for i := range left.bytes {
		left.bytes[i] = byte(i*17 + 3)
	}
	right.bytes = left.bytes
	if left.bytes != right.bytes {
		t.Fatal("identical unaligned arrays compare unequal")
	}
	for i := range right.bytes {
		right.bytes[i] ^= 0x80
		if left.bytes == right.bytes {
			t.Fatalf("comparison missed byte %d", i)
		}
		right.bytes[i] ^= 0x80
	}
	if left.bytes != right.bytes {
		t.Fatal("restored arrays compare unequal")
	}
}
