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

package main

import (
	"strings"
	"testing"
)

func TestGenerateReportSuccess(t *testing.T) {
	native := []nativeShard{
		{
			Platform:   "linux/amd64",
			Lane:       "full",
			GoVersion:  "1.27",
			ShardIndex: "0",
			ShardTotal: "2",
			Status:     "success",
		},
	}
	wasm := []wasmProfileSummary{
		{
			Profile:       "J32-GoJS",
			ShardsFound:   3,
			ShardsTotal:   3,
			Total:         100,
			Passed:        95,
			NotApplicable: 5,
			Failed:        0,
		},
	}
	rep := generateReport(native, wasm, "https://example.com/run", "fennoai", "success", "success", "success", "success")
	if strings.Contains(rep, "@fennoai") {
		t.Fatalf("expected no mention on success, got:\n%s", rep)
	}
	if !strings.Contains(rep, "All standard library tests passed successfully.") {
		t.Fatalf("expected success message, got:\n%s", rep)
	}
	if !strings.Contains(rep, "| J32-GoJS | 3/3 | 100 | 95 | 5 | 0 |") {
		t.Fatalf("missing wasm summary row:\n%s", rep)
	}
}

func TestGenerateReportFailureMentions(t *testing.T) {
	native := []nativeShard{
		{
			Platform:   "linux/amd64",
			Lane:       "full",
			GoVersion:  "1.27",
			ShardIndex: "0",
			ShardTotal: "2",
			Status:     "failure",
		},
	}
	rep := generateReport(native, nil, "", "fennoai", "failure", "success", "success", "success")
	if !strings.Contains(rep, "@fennoai") || !strings.Contains(rep, "PR") {
		t.Fatalf("expected @fennoai with PR triage instruction, got:\n%s", rep)
	}
}

func TestGenerateReportWasmFailures(t *testing.T) {
	wasm := []wasmProfileSummary{
		{
			Profile:     "W32-WASI",
			ShardsFound: 5,
			ShardsTotal: 5,
			Total:       10,
			Passed:      9,
			Failed:      1,
			FailedPkgs: []wasmPackage{
				{
					Package: "test/std/net",
					Status:  "fail",
					Reason:  "network unreachable",
				},
			},
		},
	}
	rep := generateReport(nil, wasm, "", "fennoai", "success", "failure", "success", "success")
	if !strings.Contains(rep, "@fennoai") || !strings.Contains(rep, "PR") {
		t.Fatalf("expected @fennoai with PR triage instruction, got:\n%s", rep)
	}
	if !strings.Contains(rep, "- `test/std/net`: network unreachable") {
		t.Fatalf("expected failed package in list, got:\n%s", rep)
	}
}
