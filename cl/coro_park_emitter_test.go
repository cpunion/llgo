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

package cl

import (
	"strings"
	"testing"
)

func TestCoroParkOperationStatusPlanRejectsIncompleteOrAmbiguousVocabulary(t *testing.T) {
	for _, test := range []struct {
		name     string
		normal   []uint64
		faults   []coroParkFaultRoute
		abort    uint64
		shutdown uint64
		want     string
	}{
		{name: "no normal", abort: 1, shutdown: 2, want: "no normal"},
		{name: "duplicate normal", normal: []uint64{1, 1}, abort: 2, shutdown: 3, want: "duplicates normal"},
		{name: "missing fault kind", normal: []uint64{1}, faults: []coroParkFaultRoute{{status: 2}}, abort: 3, shutdown: 4, want: "invalid fault kind"},
		{name: "unknown fault kind", normal: []uint64{1}, faults: []coroParkFaultRoute{{status: 2, kind: coroFaultLimitV1}}, abort: 3, shutdown: 4, want: "invalid fault kind"},
		{name: "fault collision", normal: []uint64{1}, faults: []coroParkFaultRoute{{status: 1, kind: 2}}, abort: 3, shutdown: 4, want: "duplicates normal"},
		{name: "duplicate fault", normal: []uint64{1}, faults: []coroParkFaultRoute{{status: 2, kind: 2}, {status: 2, kind: 3}}, abort: 3, shutdown: 4, want: "duplicates fault"},
		{name: "abort collision", normal: []uint64{1}, abort: 1, shutdown: 3, want: "duplicates normal"},
		{name: "shutdown collision", normal: []uint64{1}, abort: 2, shutdown: 2, want: "duplicates abort"},
		{name: "normal ABI overflow", normal: []uint64{maxCoroParkResumeStatus + 1}, abort: 2, shutdown: 3, want: "uint32"},
		{name: "fault ABI overflow", normal: []uint64{1}, faults: []coroParkFaultRoute{{status: maxCoroParkResumeStatus + 1, kind: 2}}, abort: 2, shutdown: 3, want: "uint32"},
		{name: "abort ABI overflow", normal: []uint64{1}, abort: maxCoroParkResumeStatus + 1, shutdown: 3, want: "uint32"},
		{name: "shutdown ABI overflow", normal: []uint64{1}, abort: 2, shutdown: maxCoroParkResumeStatus + 1, want: "uint32"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCoroParkOperationStatuses(test.normal, test.faults, test.abort, test.shutdown)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("status plan error = %v, want %q", err, test.want)
			}
		})
	}
	if err := validateCoroParkOperationStatuses(
		[]uint64{1, 2, 3},
		[]coroParkFaultRoute{{status: 4, kind: 1}},
		5,
		6,
	); err != nil {
		t.Fatalf("valid status plan: %v", err)
	}
}
