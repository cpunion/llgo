package goroot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGOROOTCaseOutcome(t *testing.T) {
	for _, expectation := range []string{"none", "xfail", "not-applicable", "flaky"} {
		for _, wasm := range []bool{false, true} {
			for _, failure := range []bool{false, true} {
				var err error
				if failure {
					err = errors.New("guest execution failed")
				}
				status, reason := gorootCaseOutcome(wasm, err, expectation == "xfail", "known compiler bug", expectation == "not-applicable", "native GC assumption", expectation == "flaky", "native scheduling assumption")
				want := "pass"
				if failure {
					want = "fail"
					if !wasm {
						switch expectation {
						case "xfail":
							want = "expected-failure"
						case "not-applicable":
							want = "not-applicable"
						case "flaky":
							want = "known-flaky"
						}
					}
				}
				if status != want || (reason == "") != (expectation == "none") {
					t.Errorf("expectation=%s wasm=%v failure=%v: (%s, %q), want %s", expectation, wasm, failure, status, reason, want)
				}
			}
		}
	}
}

func TestGOROOTReportCheckpoints(t *testing.T) {
	name := filepath.Join(t.TempDir(), "report.json")
	env := toolchainEnv{GOOS: "js", GOARCH: "wasm", GOVERSION: "go1.27.0"}
	r := newGOROOTReport(env, "EC32", 1, 4, []testCase{{RelPath: "a.go", Directive: "run"}, {RelPath: "b.go", Directive: "rundir"}})
	check := func(wantCounts map[string]int, complete bool) {
		t.Helper()
		if r.finished() != complete {
			t.Fatalf("finished=%v, want %v", r.finished(), complete)
		}
		if err := r.write(name); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			gorootReport
			Counts map[string]int `json:"counts"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Complete != complete || !reflect.DeepEqual(got.Counts, wantCounts) || !reflect.DeepEqual(got.gorootReport, *r) {
			t.Fatalf("unexpected checkpoint: %s", data)
		}
	}
	check(map[string]int{"not-run": 2}, false)
	r.Cases[0].Status = "running"
	check(map[string]int{"running": 1, "not-run": 1}, false)
	r.Cases[0].Status, r.Cases[0].Error = "fail", "actual build/run diagnostics"
	check(map[string]int{"fail": 1, "not-run": 1}, false)
	r.Cases[1].Status = "pass"
	r.Complete = true
	check(map[string]int{"fail": 1, "pass": 1}, true)
	if err := r.write(filepath.Join(t.TempDir(), "missing", "report.json")); err == nil {
		t.Fatal("unwritable report path accepted")
	}
	if err := r.write(""); err != nil {
		t.Fatal(err)
	}
}
