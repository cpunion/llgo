// Copyright 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by the Apache License, Version 2.0.
// See LICENSE for details.

package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgo-dev/llgo/internal/packages"
)

func TestTestOutputPolicy(t *testing.T) {
	for _, tc := range []struct {
		name               string
		local              bool
		count, parallelism int
		args               []string
		json, stream, show bool
	}{
		{name: "local", local: true, count: 1, stream: true},
		{name: "quiet single package", count: 1},
		{name: "verbose single package", count: 1, args: []string{"-test.v"}, stream: true, show: true},
		{name: "verbose parallel packages", count: 2, args: []string{"-test.v"}, show: true},
		{name: "verbose p1", count: 2, parallelism: 1, args: []string{"-test.v"}, stream: true, show: true},
		{name: "quiet p1", count: 2, parallelism: 1},
		{name: "json parallel packages", count: 2, json: true, stream: true},
		{name: "list single package", count: 1, args: []string{"-test.list", "Test"}, stream: true, show: true},
		{name: "list parallel packages", count: 2, args: []string{"-test.list=Test"}, show: true},
		{name: "benchmark parallel packages", count: 2, args: []string{"-test.bench=."}, stream: true},
		{name: "fuzz parallel packages", count: 2, args: []string{"-test.fuzz", "Fuzz"}, stream: true},
		{name: "help", count: 1, args: []string{"-h"}, stream: true, show: true},
		{name: "verbose disabled", count: 1, args: []string{"-test.v", "-test.v=false"}},
		{name: "flags after separator", count: 1, args: []string{"--", "-test.v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := &Config{RunArgs: tc.args, BuildParallelism: tc.parallelism, TestJSON: tc.json}
			got := newTestOutputPolicy(conf, tc.local, tc.count)
			if got.stream != tc.stream || got.show != tc.show {
				t.Fatalf("policy = %+v, want stream=%t show=%t", got, tc.stream, tc.show)
			}
		})
	}
}

func TestTestOutputPolicyPreservesOriginalSelection(t *testing.T) {
	conf := &Config{RunArgs: []string{"-test.v"}}
	initial := []*packages.Package{
		{PkgPath: "p"},
		{PkgPath: "p", ForTest: "p"},
		{PkgPath: "p_test", ForTest: "p"},
		{PkgPath: "p.test"},
		{PkgPath: "no-tests"},
	}
	configureTestOutput(conf, []string{"./..."}, initial)
	if conf.testOutput.stream {
		t.Fatal("a package without tests must still count toward a multi-package selection")
	}
	child := conf.clone()
	configureTestOutput(child, []string{"p"}, initial[:4])
	if child.testOutput.stream {
		t.Fatal("a feature group must preserve the original multi-package policy")
	}
	local := &Config{}
	configureTestOutput(local, nil, initial[:4])
	child = local.clone()
	configureTestOutput(child, []string{"p"}, initial[:4])
	if !child.testOutput.stream {
		t.Fatal("a local invocation must stay streaming when a feature group adds an explicit package")
	}
}

func TestTestJSONArgs(t *testing.T) {
	args := []string{"-test.run=Test", "-test.v", "-test.v=false", "--", "-test.v=false"}
	want := []string{"-test.run=Test", "-test.v=test2json", "--", "-test.v=false"}
	if got := testJSONArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v, want %v", got, want)
	}
	if args[1] != "-test.v" {
		t.Fatal("JSON argument normalization changed the caller's slice")
	}
}

// observedTestOutput also allows assertions while workers are writing.
type observedTestOutput struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	changed chan struct{}
}

func (w *observedTestOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	if w.changed != nil {
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (w *observedTestOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestRunTestProgramsPackageOrder(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	laterStarted := make(chan struct{})
	done := make(chan testRunResult, 1)
	stdout := &observedTestOutput{}
	go func() {
		done <- runTestPrograms([]testProgram{{pkgName: "a"}, {pkgName: "b"}, {pkgName: "c"}}, 2,
			&Config{RunArgs: []string{"-test.v"}}, stdout, io.Discard,
			func(program testProgram, output io.Writer) error {
				fmt.Fprintln(output, program.pkgName+" output")
				if program.pkgName == "a" {
					<-release
				} else if program.pkgName == "c" {
					// c can only start after b's result was reported, while a
					// is still blocked. b must not print ahead of a.
					close(laterStarted)
				}
				return nil
			})
	}()
	select {
	case <-laterStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("later package did not start while the first package was blocked")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("buffered output appeared before the first package finished: %q", got)
	}
	unblock()
	select {
	case result := <-done:
		if result.failed {
			t.Fatal("successful packages failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("packages did not finish after release")
	}
	output := stdout.String()
	if a, b, c := strings.Index(output, "a output"), strings.Index(output, "b output"), strings.Index(output, "c output"); a < 0 || a >= b || b >= c {
		t.Fatalf("packages printed out of selection order: %q", output)
	}
}

func TestRunTestProgramsQuietOutput(t *testing.T) {
	var stdout bytes.Buffer
	result := runTestPrograms([]testProgram{{pkgName: "pass"}, {pkgName: "fail"}}, 2, &Config{}, &stdout, io.Discard,
		func(program testProgram, output io.Writer) error {
			fmt.Fprintln(output, program.pkgName+" detail")
			if program.pkgName == "fail" {
				return errors.New("failure")
			}
			return nil
		})
	if !result.failed || strings.Contains(stdout.String(), "pass detail") || !strings.Contains(stdout.String(), "fail detail") {
		t.Fatalf("quiet run did not hide success output and preserve failure output: %+v %q", result, stdout.String())
	}
}

func TestRunTestProgramsParallelJSON(t *testing.T) {
	var stdout bytes.Buffer
	result := runTestPrograms([]testProgram{{pkgName: "pass"}, {pkgName: "fail"}}, 2, &Config{TestJSON: true}, &stdout, io.Discard,
		func(program testProgram, output io.Writer) error {
			for i := 0; i < 100; i++ {
				fmt.Fprintf(output, "%s line %d\n", program.pkgName, i)
			}
			if program.pkgName == "fail" {
				return errors.New("failure")
			}
			return nil // TestMain may exit successfully without printing PASS.
		})
	if !result.failed {
		t.Fatal("failing JSON package reported success")
	}
	counts := make(map[string]int)
	actions := make(map[string]string)
	for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
		var event struct{ Action, Package, Test, Output string }
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("interleaved JSON record: %q: %v", line, err)
		}
		if event.Package != "pass" && event.Package != "fail" {
			t.Fatalf("missing package attribution: %s", line)
		}
		if strings.Contains(event.Output, " line ") {
			if !strings.HasPrefix(event.Output, event.Package+" line ") {
				t.Fatalf("wrong package attribution: %s", line)
			}
			counts[event.Package]++
		}
		if event.Test == "" && (event.Action == "pass" || event.Action == "fail") {
			actions[event.Package] = event.Action
		}
	}
	if counts["pass"] != 100 || counts["fail"] != 100 || actions["pass"] != "pass" || actions["fail"] != "fail" {
		t.Fatalf("lost output or process exit result: counts=%v actions=%v", counts, actions)
	}
}

func TestTestResultReporterSkippedRoot(t *testing.T) {
	var stdout bytes.Buffer
	report := testResultReporter(2, &stdout)
	report(1, &testProgramResult{output: []byte("second\n")})
	report(0, nil)
	if stdout.String() != "second\n" {
		t.Fatalf("skipped root blocked later output: %q", stdout.String())
	}
}
