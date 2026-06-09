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

package gotest

import (
	"strings"
	"testing"
)

const recoverThenDeferredPanicMainProbe = `package main

func end() {
	if recovered := recover(); recovered != nil {
		defer panic(recovered)
		println("will panic in defer")
	}
	println("end")
}

func main() {
	defer end()
	panic("panic in main")
}
`

func TestRecoverThenDeferredPanic(t *testing.T) {
	var events []string
	got := recoverThenDeferredPanic(&events)
	if got != "panic in body" {
		t.Fatalf("recover = %v, want panic in body", got)
	}
	want := []string{"inner recovered", "inner end", "outer recovered"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestRecoverThenDeferredPanicIRTerminatesBlocks(t *testing.T) {
	ir := llgoIRFromProbe(t, "recover-then-deferred-panic", recoverThenDeferredPanicMainProbe)
	assertNoInstructionsAfterUnreachable(t, ir)
}

func recoverThenDeferredPanic(events *[]string) (recovered any) {
	defer func() {
		recovered = recover()
		*events = append(*events, "outer recovered")
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				defer panic(r)
				*events = append(*events, "inner recovered")
			}
			*events = append(*events, "inner end")
		}()
		panic("panic in body")
	}()
	return nil
}

func assertNoInstructionsAfterUnreachable(t *testing.T, ir string) {
	t.Helper()

	lines := strings.Split(ir, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "unreachable" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				continue
			}
			if trimmed == "}" || isLLVMBasicBlockLabel(trimmed) {
				break
			}
			t.Fatalf("instruction after unreachable at IR line %d: %s", j+1, trimmed)
		}
	}
}

func isLLVMBasicBlockLabel(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return false
	}
	return !strings.ContainsAny(line[:colon], " \t")
}
