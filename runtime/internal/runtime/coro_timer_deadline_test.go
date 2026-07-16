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

import "testing"

func TestCoroTimerDeadlineAfterV1(t *testing.T) {
	tests := []struct {
		name  string
		now   int64
		delay int64
		want  int64
		ok    bool
	}{
		{name: "negative-now", now: -1, delay: 1},
		{name: "negative-delay-is-due", now: 7, delay: -1, want: 7, ok: true},
		{name: "zero-delay-is-due", now: 7, want: 7, ok: true},
		{name: "positive", now: 7, delay: 11, want: 18, ok: true},
		{name: "exact-maximum", now: coroTimerMaxDeadlineV1 - 1, delay: 1, want: coroTimerMaxDeadlineV1, ok: true},
		{name: "overflow-saturates", now: coroTimerMaxDeadlineV1 - 1, delay: 2, want: coroTimerMaxDeadlineV1, ok: true},
		{name: "maximum-now-saturates", now: coroTimerMaxDeadlineV1, delay: coroTimerMaxDeadlineV1, want: coroTimerMaxDeadlineV1, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := coroTimerDeadlineAfterV1(test.now, test.delay)
			if ok != test.ok || got != test.want {
				t.Fatalf("deadline after (%d, %d) = (%d, %t), want (%d, %t)", test.now, test.delay, got, ok, test.want, test.ok)
			}
		})
	}
}
