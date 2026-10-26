//go:build !llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const coroInlineNativeFaultFixture = `package main
import (
 "os"
 "runtime"
 "unsafe"
)
var observed byte
var conditionalYield bool
var forwardingVisits int
//go:noinline
func faultLeaf() { observed = *(*byte)(unsafe.Pointer(uintptr(1))) }
// A source-visible yield branch keeps this a physical coroutine, while the
// ordinary execution faults before any scheduler handoff can protect it.
//go:noinline
func forwardedFaultLeaf() {
 if conditionalYield { runtime.Gosched() }
 observed = *(*byte)(unsafe.Pointer(uintptr(1)))
}
func forwardFault() { forwardedFaultLeaf() }
func forwardFaultTwice() { forwardFault() }
func forwardingVisit() { forwardingVisits++ }
// This caller must own a frame, rather than transparently forwarding too.
func forwardedCall() { forwardingVisit(); forwardFaultTwice() }
//go:noinline
func direct() { faultLeaf() }
//go:noinline
func nested() { direct() }
//go:noinline
func deferred() { defer faultLeaf() }
//go:noinline
func deferredValue() { f:=faultLeaf; defer f() }
type runner struct{}
//go:noinline
func (*runner) Run() { faultLeaf() }
//go:noinline
func method() { new(runner).Run() }
var indirect interface{Run()}
//go:noinline
func throughInterface() { indirect.Run() }
//go:noinline
func invoke(f func()) { f() }
//go:noinline
func throughValue() { invoke(faultLeaf) }
//go:noinline
func yielded() { runtime.Gosched(); faultLeaf() }
//go:noinline
func parked() { ch:=make(chan struct{});go func(){close(ch)}();<-ch;faultLeaf() }
//go:noinline
func catches(f func()) (caught bool) {
 defer func(){ if recover()!=nil {caught=true} }()
 f()
 return false
}
func main() {
 indirect=new(runner)
 conditionalYield=len(os.Args)>100
 cases:=[]func(){faultLeaf,direct,nested,deferred,deferredValue,method,throughInterface,throughValue,yielded,parked,forwardedCall}
 for _,f:=range cases {
  if !catches(f) {panic("inline native fault was not recovered")}
  runtime.GC()
 }
 if forwardingVisits!=1 {panic("forwarding caller lost its side effect")}
 println("ok inline native fault",len(cases),observed,forwardingVisits)
}
`

func TestCoroInlineNativeFaultAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_INLINE_FAULT_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_INLINE_FAULT_ACCEPTANCE=1 for native signal acceptance")
	}
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		t.Skip("native POSIX coroutine signal target")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(coroInlineNativeFaultFixture), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, runtimeRun bool, args ...string) string {
		t.Helper()
		deadline := 3 * time.Minute
		if runtimeRun {
			deadline = 30 * time.Second
		}
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), deadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = root
		if !runtimeRun {
			cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "GOMEMLIMIT=2GiB")
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, output)
		}
		return strings.TrimSpace(string(output))
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		compiler = filepath.Join(dir, "llgo")
		run(t, false, filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-p=1", "-o", compiler, "./cmd/llgo")
	}
	oracle := filepath.Join(dir, "official-go")
	run(t, false, filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-p=1", "-o", oracle, source)
	const want = "ok inline native fault 11 0 1"
	for _, procs := range []string{"1", "4"} {
		t.Setenv("GOMAXPROCS", procs)
		if got := run(t, true, oracle); got != want {
			t.Fatalf("Go oracle P%s=%q want%q", procs, got, want)
		}
	}
	for _, mode := range []string{"embedded", "external", "none"} {
		t.Run(mode, func(t *testing.T) {
			image := filepath.Join(dir, "llgo-"+mode)
			run(t, false, compiler, "build", "-p=1", "-Os", "-trimpath", "-ldflags=-s -w", "-pclntab="+mode, "-o", image, source)
			if mode == "external" {
				// No metadata file is available while native faults are recovered.
				if err := os.Rename(image+".pclntab", image+".pclntab.withheld"); err != nil {
					t.Fatal(err)
				}
			}
			for _, procs := range []string{"1", "4"} {
				t.Setenv("GOMAXPROCS", procs)
				for repeat := 0; repeat < 3; repeat++ {
					if got := run(t, true, image); got != want {
						t.Fatalf("%s P%s repeat%d=%q want%q", mode, procs, repeat, got, want)
					}
				}
			}
		})
	}
}
