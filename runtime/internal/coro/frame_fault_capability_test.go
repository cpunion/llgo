// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

import (
	"testing"
	"unsafe"
)

func TestDescriptorNativeFaultCapability(t *testing.T) {
	for _, test := range []struct {
		name                     string
		flags                    uint32
		context, boundary, valid bool
	}{
		{"legacy", 0, true, true, true}, {"no-context", 2, false, true, true},
		{"no-fault", 4, true, false, true}, {"neither", 6, false, false, true},
		{"unknown", 8, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor := FrameDescriptorV1{Version: 1, Flags: test.flags}
			mode, ok := descriptorRuntimeContextMode(unsafe.Pointer(&descriptor))
			if ok != test.valid {
				t.Fatalf("descriptor admission=%t want %t", ok, test.valid)
			}
			if !ok {
				return
			}
			p := new(P)
			driver, _, _ := bindTestExecutorDriver(t, p)
			task := newYieldingTestGConfigured(t, test.name, true)
			task.frame.header.Descriptor = unsafe.Pointer(&descriptor)
			task.g.active.runtimeContext = mode
			if !Enqueue(p, task.g) {
				t.Fatal("enqueue")
			}
			_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
			_, context, boundary, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
			if !ok || context != test.context || boundary != test.boundary {
				t.Fatalf("resume=(context=%t,boundary=%t,ok=%t) want (%t,%t,true)", context, boundary, ok, test.context, test.boundary)
			}
		})
	}
}

func TestIssuedResumeUsesFrameNativeFaultCapability(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, capability := range []bool{false, true} {
			for _, flags := range []uint32{0, 2, 4, 6} {
				descriptor := &FrameDescriptorV1{Version: 1, Flags: flags}
				p := new(P)
				driver, _, _ := bindTestExecutorDriver(t, p)
				task := newYieldingTestGConfigured(t, "frame-boundary", capability)
				task.frame.header.Descriptor = unsafe.Pointer(descriptor)
				if cached {
					mode, ok := descriptorRuntimeContextMode(unsafe.Pointer(descriptor))
					if !ok {
						t.Fatalf("flags=%d rejected", flags)
					}
					task.g.active.runtimeContext = mode
				}
				if !Enqueue(p, task.g) {
					t.Fatal("enqueue")
				}
				_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
				_, context, boundary, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
				if !ok || context != (flags&2 == 0) || boundary != (capability && flags&4 == 0) {
					t.Fatalf("cached=%t cap=%t flags=%d: (%t,%t,%t)", cached, capability, flags, context, boundary, ok)
				}
			}
		}
	}
}

func TestMalformedFrameCapabilityRetainsBoundaries(t *testing.T) {
	for _, mode := range []frameRuntimeContextMode{0, 3, 4, 7, 8, 255} {
		if !mode.requiresRuntimeContext() || mode.excludesNativeFaultBoundary() {
			t.Fatalf("malformed/unknown mode %d suppressed a boundary", mode)
		}
	}
}
