/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package gotest

import (
	"runtime"
	"strings"
	"testing"
)

//go:noinline
func callerStackExhausted() bool {
	if pc, _, _, ok := runtime.Caller(0); !ok || pc == 0 {
		return false
	}
	var pcs [128]uintptr
	n := runtime.Callers(0, pcs[:])
	if n == 0 || n == len(pcs) {
		return false
	}
	// Callers includes its own frame; Caller(0) starts with this function.
	if _, _, _, ok := runtime.Caller(n - 1); ok {
		return false
	}
	if _, _, _, ok := runtime.Caller(1 << 20); ok {
		return false
	}
	if runtime.Callers(n, pcs[:]) != 0 || runtime.Callers(1<<20, pcs[:]) != 0 {
		return false
	}
	frames := runtime.CallersFrames(pcs[:runtime.Callers(0, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "/runtime/internal/runtime.coro") {
			return false
		}
		if !more {
			return true
		}
	}
}

func TestRuntimeCallerLogicalStackExhaustion(t *testing.T) {
	if !callerStackExhausted() {
		t.Fatal("ordinary logical stack falls through into executor frames")
	}
	result := make(chan bool)
	go func() {
		defer func() {
			before := callerStackExhausted()
			value := recover()
			after := callerStackExhausted()
			result <- before && value == "caller-exhaustion" && after
		}()
		panic("caller-exhaustion")
	}()
	if !<-result {
		t.Fatal("panic/recover logical stack falls through into executor frames")
	}
}
