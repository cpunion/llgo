package goroot

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A test process timeout does not run deferred functions. Persist every case
// independently so an interrupted shard retains evidence and pending cases.
type gorootReport struct {
	GoVersion string             `json:"go_version"`
	Platform  string             `json:"platform"`
	Profile   string             `json:"profile,omitempty"`
	Shard     int                `json:"shard"`
	Shards    int                `json:"shards"`
	Complete  bool               `json:"complete"`
	Cases     []gorootCaseResult `json:"cases"`
}

type gorootCaseResult struct {
	Path            string  `json:"path"`
	Directive       string  `json:"directive"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Error           string  `json:"error,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
}

func newGOROOTReport(env toolchainEnv, profile string, shard, shards int, cases []testCase) *gorootReport {
	r := &gorootReport{GoVersion: env.GOVERSION, Platform: env.GOOS + "/" + env.GOARCH, Profile: profile, Shard: shard, Shards: shards}
	for _, tc := range cases {
		r.Cases = append(r.Cases, gorootCaseResult{Path: tc.RelPath, Directive: tc.Directive, Status: "not-run"})
	}
	return r
}

func (r *gorootReport) counts() map[string]int {
	counts := make(map[string]int)
	for _, c := range r.Cases {
		counts[c.Status]++
	}
	return counts
}

func (r *gorootReport) finished() bool {
	for _, c := range r.Cases {
		if c.Status == "not-run" || c.Status == "running" {
			return false
		}
	}
	return true
}

func (r *gorootReport) write(name string) error {
	if name == "" {
		return nil
	}
	data, err := json.MarshalIndent(struct {
		*gorootReport
		Counts map[string]int `json:"counts"`
	}{r, r.counts()}, "", "  ")
	if err != nil {
		return err
	}
	// Replace atomically: a killed process must not destroy the prior report.
	f, err := os.CreateTemp(filepath.Dir(name), ".goroot-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func gorootCaseOutcome(wasm bool, err error, xfail bool, xfailReason string, notApply bool, notApplyReason string, flaky bool, flakyReason string, wasmXFail bool) (status, reason string) {
	status = "fail"
	switch {
	case xfail:
		status, reason = "expected-failure", xfailReason
	case notApply:
		status, reason = "not-applicable", notApplyReason
	case flaky:
		status, reason = "known-flaky", flakyReason
	}
	if err == nil {
		return "pass", reason
	}
	if wasm && !notApply && !(xfail && wasmXFail) {
		return "fail", reason
	}
	return status, reason
}
