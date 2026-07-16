//go:build (darwin || linux) && !baremetal

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

package corodoorbell

import "testing"

func TestDeadlinePollTimeout(t *testing.T) {
	maxInt64 := int64(^uint64(0) >> 1)
	tests := []struct {
		name     string
		now      int64
		deadline int64
		timeout  int32
		reached  bool
		ok       bool
	}{
		{name: "zero", ok: true, reached: true},
		{name: "past", now: 7, deadline: 6, ok: true, reached: true},
		{name: "exact", now: 7, deadline: 7, ok: true, reached: true},
		{name: "one nanosecond", deadline: 1, timeout: 1, ok: true},
		{name: "one millisecond", deadline: 1_000_000, timeout: 1, ok: true},
		{name: "round upward", deadline: 1_000_001, timeout: 2, ok: true},
		{name: "relative offset", now: 9_000_000, deadline: 10_000_001, timeout: 2, ok: true},
		{name: "bounded", deadline: 1_000_000_001, timeout: physicalPollMaxMS, ok: true},
		{name: "maximum", deadline: maxInt64, timeout: physicalPollMaxMS, ok: true},
		{name: "maximum near", now: maxInt64 - 1, deadline: maxInt64, timeout: 1, ok: true},
		{name: "negative now", now: -1},
		{name: "negative deadline", deadline: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timeout, reached, ok := deadlinePollTimeout(test.now, test.deadline)
			if timeout != test.timeout || reached != test.reached || ok != test.ok {
				t.Fatalf("deadlinePollTimeout(%d, %d) = (%d, %t, %t), want (%d, %t, %t)",
					test.now, test.deadline, timeout, reached, ok, test.timeout, test.reached, test.ok)
			}
		})
	}
}
