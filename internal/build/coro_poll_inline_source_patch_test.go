//go:build !llgo

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

package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/env"
	llruntime "github.com/goplus/llgo/runtime"
)

func TestNativeCoroInternalPollReadWriteSourcePatch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("native coroutine internal/poll patch requires Darwin or Linux")
	}
	buildFlags := []string{"-tags=llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc"}
	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos: runtime.GOOS, goarch: runtime.GOARCH, buildFlags: buildFlags,
	})
	if err != nil {
		t.Fatal(err)
	}
	pollDir := filepath.Join(runtime.GOROOT(), "src", "internal", "poll")
	patchFile := filepath.Join(pollDir, "z_llgo_patch_fd_unix_coro_native_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing native coroutine internal/poll patch %s", patchFile)
	}
	for _, marker := range []string{
		"func (fd *FD) Read(p []byte) (int, error)",
		"func (fd *FD) Write(p []byte) (int, error)",
		"func runtime_pollReadAttempt(",
		"func runtime_pollWriteAttempt(",
		"return syscall.Read(fd.Sysfd, p)",
		"return syscall.Write(fd.Sysfd, p)",
	} {
		if !strings.Contains(string(patch), marker) {
			t.Errorf("native coroutine internal/poll patch lacks %q", marker)
		}
	}
	for _, forbidden := range []string{
		"func (fd *FD) SetBlocking() error",
		"func (fd *FD) RawControl(f func(uintptr)) error",
		"func (fd *FD) RawRead(f func(uintptr) bool) error",
		"func (fd *FD) RawWrite(f func(uintptr) bool) error",
		"func (fd *FD) Dup() (int, string, error)",
		"runtime_pollRetireNonblockingAttempts",
		"NonblockingLease",
	} {
		if strings.Contains(string(patch), forbidden) {
			t.Errorf("native coroutine internal/poll patch retains obsolete marker %q", forbidden)
		}
	}

	stdlibFDUnix := filepath.Join(pollDir, "fd_unix.go")
	filtered, ok := overlay[stdlibFDUnix]
	if !ok {
		t.Fatalf("internal/poll patch did not filter %s", stdlibFDUnix)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), stdlibFDUnix, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered internal/poll/fd_unix.go: %v", err)
	}
	assertPatchedPollMethodsRemoved(t, stdlibFDUnix, parsed, []string{"Read", "Write"})
	assertPollMethodsPresent(t, stdlibFDUnix, parsed, []string{"SetBlocking", "Dup", "RawRead", "RawWrite"})

	stdlibFDPosix := filepath.Join(pollDir, "fd_posix.go")
	if _, ok := overlay[stdlibFDPosix]; ok {
		t.Fatalf("read/write-only internal/poll patch unexpectedly filtered %s", stdlibFDPosix)
	}
	hostNetPatch := filepath.Join(runtime.GOROOT(), "src", "net", "z_llgo_patch_fd_unix_coro_host_llgo.go")
	if _, ok := overlay[hostNetPatch]; ok {
		t.Fatalf("native target unexpectedly selected host net patch %s", hostNetPatch)
	}
	if !llruntime.HasSourcePatchPkg("internal/poll") || llruntime.HasAltPkg("internal/poll") {
		t.Fatalf("internal/poll patch registration = source:%t alt:%t", llruntime.HasSourcePatchPkg("internal/poll"), llruntime.HasAltPkg("internal/poll"))
	}
}

func assertPollMethodsPresent(t *testing.T, path string, file *ast.File, names []string) {
	t.Helper()
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil {
			continue
		}
		if _, tracked := wanted[function.Name.Name]; tracked {
			wanted[function.Name.Name] = true
		}
	}
	for name, present := range wanted {
		if !present {
			t.Errorf("filtered %s lost unpatched FD.%s", path, name)
		}
	}
}

func assertPatchedPollMethodsRemoved(t *testing.T, path string, file *ast.File, names []string) {
	t.Helper()
	tracked := make(map[string]bool, len(names))
	for _, name := range names {
		tracked[name] = false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil {
			continue
		}
		if _, wanted := tracked[function.Name.Name]; wanted {
			tracked[function.Name.Name] = true
		}
	}
	for name, present := range tracked {
		if present {
			t.Errorf("filtered %s retained FD.%s", path, name)
		}
	}
}

func TestNativeCoroInternalPollPatchIsCapabilityGated(t *testing.T) {
	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos: runtime.GOOS, goarch: runtime.GOARCH,
		buildFlags: []string{"-tags=llgo,llgo_coro,llgo_coro_native_pipe,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	patchFile := filepath.Join(runtime.GOROOT(), "src", "internal", "poll", "z_llgo_patch_fd_unix_coro_native_llgo.go")
	if _, ok := overlay[patchFile]; ok {
		t.Fatalf("internal/poll inline-attempt patch selected without native timer capability: %s", patchFile)
	}
}

func TestHostCoroInternalPollAndNetSourcePatch(t *testing.T) {
	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       "linux",
		goarch:     "arm",
		buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm,wasip2,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pollDir := filepath.Join(runtime.GOROOT(), "src", "internal", "poll")
	patchFile := filepath.Join(pollDir, "z_llgo_patch_fd_unix_coro_host_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing host coroutine internal/poll patch %s", patchFile)
	}
	for _, marker := range []string{
		"func (fd *FD) Read(p []byte) (int, error)",
		"func (fd *FD) ReadFrom(p []byte) (int, syscall.Sockaddr, error)",
		"func (fd *FD) ReadFromInet4(p []byte, from *syscall.SockaddrInet4) (int, error)",
		"func (fd *FD) ReadMsg(p []byte, oob []byte, flags int)",
		"func (fd *FD) ReadMsgInet4(",
		"func (fd *FD) Write(p []byte) (int, error)",
		"func (fd *FD) WriteTo(p []byte, peer syscall.Sockaddr) (int, error)",
		"func (fd *FD) WriteToInet4(p []byte, peer *syscall.SockaddrInet4) (int, error)",
		"func (fd *FD) WriteMsg(p []byte, oob []byte, peer syscall.Sockaddr)",
		"func (fd *FD) WriteMsgInet4(",
		"func (fd *FD) Accept() (int, syscall.Sockaddr, string, error)",
		"llgo.coroHostOperation",
		"llgoCoroHostDeadlineFlagV1",
		"runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'r')",
		"runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'w')",
		"controlEpoch, fd uintptr",
		"err == syscall.ETIMEDOUT",
	} {
		if !strings.Contains(string(patch), marker) {
			t.Errorf("host coroutine internal/poll patch lacks %q", marker)
		}
	}
	for _, forbidden := range []string{
		"runtime_pollReadAttempt",
		"runtime_pollWriteAttempt",
		"llgo.coroWorker",
		"make(chan",
		"go func",
	} {
		if strings.Contains(string(patch), forbidden) {
			t.Errorf("host coroutine internal/poll patch retains incompatible marker %q", forbidden)
		}
	}

	stdlibFDUnix := filepath.Join(pollDir, "fd_unix.go")
	filtered, ok := overlay[stdlibFDUnix]
	if !ok {
		t.Fatalf("host internal/poll patch did not filter %s", stdlibFDUnix)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), stdlibFDUnix, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered internal/poll/fd_unix.go: %v", err)
	}
	assertPatchedPollMethodsRemoved(t, stdlibFDUnix, parsed, []string{
		"Read", "ReadFrom", "ReadFromInet4", "ReadFromInet6",
		"ReadMsg", "ReadMsgInet4", "ReadMsgInet6",
		"Write", "WriteTo", "WriteToInet4", "WriteToInet6",
		"WriteMsg", "WriteMsgInet4", "WriteMsgInet6", "Accept",
	})
	assertPollMethodsPresent(t, stdlibFDUnix, parsed, []string{"SetBlocking", "Dup", "RawRead", "RawWrite"})

	nativePatch := filepath.Join(pollDir, "z_llgo_patch_fd_unix_coro_native_llgo.go")
	if _, ok := overlay[nativePatch]; ok {
		t.Fatalf("host target unexpectedly selected native internal/poll patch %s", nativePatch)
	}
	if !llruntime.HasSourcePatchPkg("internal/poll") || llruntime.HasAltPkg("internal/poll") {
		t.Fatalf("internal/poll patch registration = source:%t alt:%t", llruntime.HasSourcePatchPkg("internal/poll"), llruntime.HasAltPkg("internal/poll"))
	}

	netDir := filepath.Join(runtime.GOROOT(), "src", "net")
	netPatchFile := filepath.Join(netDir, "z_llgo_patch_fd_unix_coro_host_llgo.go")
	netPatch, ok := overlay[netPatchFile]
	if !ok {
		t.Fatalf("missing host coroutine net patch %s", netPatchFile)
	}
	for _, marker := range []string{
		"func (fd *netFD) connect(",
		"fd.pfd.Init(fd.net, true)",
		"context.AfterFunc(",
		"fd.pfd.Connect(ra)",
		"fd.pfd.SetWriteDeadline(",
	} {
		if !strings.Contains(string(netPatch), marker) {
			t.Errorf("host coroutine net patch lacks %q", marker)
		}
	}
	if strings.Contains(string(netPatch), "connectFunc(") {
		t.Error("host coroutine net patch retained the pre-poll blocking connect path")
	}
	stdlibNetFDUnix := filepath.Join(netDir, "fd_unix.go")
	filteredNet, ok := overlay[stdlibNetFDUnix]
	if !ok {
		t.Fatalf("host net patch did not filter %s", stdlibNetFDUnix)
	}
	parsedNet, err := parser.ParseFile(token.NewFileSet(), stdlibNetFDUnix, filteredNet, 0)
	if err != nil {
		t.Fatalf("parse filtered net/fd_unix.go: %v", err)
	}
	assertPatchedPollMethodsRemoved(t, stdlibNetFDUnix, parsedNet, []string{"connect"})
	if !llruntime.HasSourcePatchPkg("net") || llruntime.HasAltPkg("net") {
		t.Fatalf("net patch registration = source:%t alt:%t", llruntime.HasSourcePatchPkg("net"), llruntime.HasAltPkg("net"))
	}
}
