//go:build !llgo && (darwin || linux) && !baremetal

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

package coroclock

import "testing"

func TestComposeMonotonicNano(t *testing.T) {
	maximumSeconds := monotonicMaxInt64 / monotonicNanosPerSecond
	maximumNanoseconds := monotonicMaxInt64 % monotonicNanosPerSecond
	tests := []struct {
		name        string
		seconds     int64
		nanoseconds int64
		want        int64
		ok          bool
	}{
		{name: "zero", ok: true},
		{name: "normalized", seconds: 12, nanoseconds: 345, want: 12_000_000_345, ok: true},
		{name: "maximum", seconds: maximumSeconds, nanoseconds: maximumNanoseconds, want: monotonicMaxInt64, ok: true},
		{name: "negative seconds", seconds: -1},
		{name: "negative nanoseconds", nanoseconds: -1},
		{name: "unnormalized nanoseconds", nanoseconds: monotonicNanosPerSecond},
		{name: "seconds overflow", seconds: maximumSeconds + 1},
		{name: "nanoseconds overflow", seconds: maximumSeconds, nanoseconds: maximumNanoseconds + 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := composeMonotonicNano(test.seconds, test.nanoseconds)
			if got != test.want || ok != test.ok {
				t.Fatalf("composeMonotonicNano(%d, %d) = %d, %t; want %d, %t",
					test.seconds, test.nanoseconds, got, ok, test.want, test.ok)
			}
		})
	}
}
