//go:build coro_run_decision_abi_test

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
	"testing"
	"unsafe"
)

func coroRuntimeAbort(message string) {
	panic(message)
}

func expectCoroRunDecisionAbort(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("run-decision ABI did not abort")
		}
	}()
	call()
}

func TestCoroRunDecisionOutputModeV1(t *testing.T) {
	g := unsafe.Pointer(new(byte))
	if mode := coroRunDecisionOutputModeOfV1(g, nil, nil, nil, nil, nil); mode != coroRunDecisionOutputNormalOnlyV1 {
		t.Fatalf("all-nil output mode = %d, want normal-only", mode)
	}
	words := [5]uint32{}
	if mode := coroRunDecisionOutputModeOfV1(g, &words[0], &words[1], &words[2], &words[3], &words[4]); mode != coroRunDecisionOutputWordsV1 {
		t.Fatalf("distinct output mode = %d, want words", mode)
	}
	if mode := coroRunDecisionOutputModeOfV1(g, &words[0], nil, &words[2], &words[3], &words[4]); mode != coroRunDecisionOutputInvalidV1 {
		t.Fatalf("partial-nil output mode = %d, want invalid", mode)
	}
	if mode := coroRunDecisionOutputModeOfV1(g, &words[0], &words[0], &words[2], &words[3], &words[4]); mode != coroRunDecisionOutputInvalidV1 {
		t.Fatalf("aliased output mode = %d, want invalid", mode)
	}
	if mode := coroRunDecisionOutputModeOfV1(nil, nil, nil, nil, nil, nil); mode != coroRunDecisionOutputInvalidV1 {
		t.Fatalf("nil-G output mode = %d, want invalid", mode)
	}
}

func TestNormalCoroRunDecisionWordsV1(t *testing.T) {
	if !normalCoroRunDecisionWordsV1(0, 0, 0, 0, 0, true) {
		t.Fatal("rejected all-zero normal decision")
	}
	if normalCoroRunDecisionWordsV1(0, 0, 0, 0, 0, false) {
		t.Fatal("accepted failed normal decision take")
	}
	for index := 0; index < 5; index++ {
		words := [5]uint32{}
		words[index] = 1
		if normalCoroRunDecisionWordsV1(words[0], words[1], words[2], words[3], words[4], true) {
			t.Fatalf("accepted non-normal decision word %d", index)
		}
	}
}

func TestCoroRunDecisionWrapperRejectsMalformedNormalOnlyMode(t *testing.T) {
	g := unsafe.Pointer(new(byte))
	expectCoroRunDecisionAbort(t, func() {
		__llgo_coro_run_decision_take_v1(g, 1, 1, nil, nil, nil, nil, nil)
	})
	word := new(uint32)
	expectCoroRunDecisionAbort(t, func() {
		__llgo_coro_run_decision_take_v1(g, 0, 0, word, nil, nil, nil, nil)
	})
}
