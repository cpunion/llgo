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
	stdcontext "context"
	"fmt"
	goimporter "go/importer"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	llssa "github.com/goplus/llgo/ssa"
)

var (
	coroNativeFleetE2ERuntimeArchiveOnce sync.Once
	coroNativeFleetE2ERuntimeArchive     string
)

const coroNativeFleetE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var ChildPasses uint32
var MainStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	ChildPasses = 0
	MainStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	for ChildPasses < 700000 {
		ChildPasses++
	}
	ChildStage = 0x1234abcd
}

func main() {
	MainStage = 1
	go child()
	for ChildStage != 0x1234abcd {
	}
	MainStage = 2
}

func Check() int32 {
	if ChildStage != 0x1234abcd || ChildPasses != 700000 || MainStage != 2 {
		return 17
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 18
	}
	return 0
}
`

const coroNativeFleetShutdownE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var MainStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	MainStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	ChildStage = 1
	for {
	}
}

func main() {
	MainStage = 1
	go child()
	for ChildStage == 0 {
	}
	MainStage = 2
}

func Check() int32 {
	if ChildStage != 1 || MainStage != 2 {
		return 27
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 28
	}
	return 0
}
`

const coroNativeFleetPeerSpawnE2ESource = `package main

import _ "unsafe"

var ChildStage uint32
var GrandchildStage uint32
var Ack chan uint32
var MainThread uintptr
var ChildThread uintptr
var GrandchildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	ChildStage = 0
	GrandchildStage = 0
	Ack = make(chan uint32)
	MainThread = threadID()
	ChildThread = 0
	GrandchildThread = 0
}

func grandchild() {
	thread := threadID()
	if thread != MainThread {
		go grandchild()
		return
	}
	GrandchildThread = thread
	GrandchildStage = 1
	Ack <- 1
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	go grandchild()
	ChildStage = 1
	Ack <- 2
}

func main() {
	go child()
	<-Ack
	<-Ack
}

func Check() int32 {
	if ChildStage != 1 || GrandchildStage != 1 {
		return 37
	}
	if MainThread == 0 || ChildThread == 0 || GrandchildThread == 0 ||
		MainThread == ChildThread || MainThread != GrandchildThread {
		return 38
	}
	return 0
}
`

const coroNativeFleetChannelSelectE2ESource = `package main

import _ "unsafe"

var Data chan uint32
var Never chan uint32
var Ack chan uint32
var Got uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	Data = make(chan uint32)
	Never = make(chan uint32)
	Ack = make(chan uint32)
	Got = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	select {
	case Got = <-Data:
	case Got = <-Never:
	}
	Ack <- 1
}

func main() {
	go child()
	Data <- 0xdecafbad
	<-Ack
}

func Check() int32 {
	if Got != 0xdecafbad {
		return 47
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 48
	}
	return 0
}
`

const coroNativeFleetChannelShutdownE2ESource = `package main

import _ "unsafe"

var NeverA chan uint32
var NeverB chan uint32
var ChildStage uint32
var MainThread uintptr
var ChildThread uintptr

//llgo:coro noblock
//go:linkname threadID C.__llgo_coro_native_fleet_e2e_thread_id_v1
func threadID() uintptr

func Setup() {
	NeverA = make(chan uint32)
	NeverB = make(chan uint32)
	ChildStage = 0
	MainThread = threadID()
	ChildThread = 0
}

func child() {
	thread := threadID()
	if thread == MainThread {
		go child()
		return
	}
	ChildThread = thread
	ChildStage = 1
	select {
	case <-NeverA:
	case <-NeverB:
	}
	ChildStage = 2
}

func main() {
	go child()
	for ChildStage == 0 {
	}
}

func Check() int32 {
	if ChildStage != 1 {
		return 57
	}
	if MainThread == 0 || ChildThread == 0 || MainThread == ChildThread {
		return 58
	}
	return 0
}
`

const coroNativeFleetSingleExecutionQuotaE2ESource = `package main

var Data chan uint32
var Got uint32

func Setup() {
	Data = make(chan uint32)
	Got = 0
}

func child() {
	Data <- 0x51a91e
}

func main() {
	go child()
	Got = <-Data
}

func Check() int32 {
	if Got != 0x51a91e {
		return 67
	}
	return 0
}
`

const coroNativeFleetGOMAXPROCSE2ESource = `package main

import _ "unsafe"

var Done chan uint32
var Failed uint32

//llgo:coro noblock
//go:linkname gomaxprocs command-line-arguments.CoroGOMAXPROCS
func gomaxprocs(int) int

//llgo:coro noblock
//go:linkname quotaReset C.__llgo_coro_native_fleet_e2e_quota_reset_v1
func quotaReset()

//llgo:coro noblock
//go:linkname quotaRun C.__llgo_coro_native_fleet_e2e_quota_run_v1
func quotaRun(uint32)

//llgo:coro noblock
//go:linkname quotaMaximum C.__llgo_coro_native_fleet_e2e_quota_maximum_v1
func quotaMaximum() uint32

func Setup() {
	Done = make(chan uint32)
	Failed = 0
}

func child() {
	quotaRun(12000000)
	Done <- 1
}

func runPhase() uint32 {
	quotaReset()
	for index := 0; index < 8; index++ {
		go child()
	}
	for index := 0; index < 8; index++ {
		<-Done
	}
	return quotaMaximum()
}

func main() {
	if gomaxprocs(0) != 1 {
		Failed = 71
		return
	}
	if gomaxprocs(4) != 1 || gomaxprocs(0) != 4 {
		Failed = 72
		return
	}
	maximum := runPhase()
	if maximum < 2 || maximum > 4 {
		Failed = 73
		return
	}
	if gomaxprocs(1) != 4 || gomaxprocs(0) != 1 {
		Failed = 74
		return
	}
	if maximum = runPhase(); maximum != 1 {
		Failed = 75
	}
}

func Check() int32 {
	return int32(Failed)
}
`

func TestCoroNativeFleetPhysicalPeerRunsDistributedChildE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetE2ESource, "distributed-child", false, 8)
}

func TestCoroNativeFleetMainReturnCancelsPeerE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetShutdownE2ESource, "main-return-cancel", false, 4)
}

func TestCoroNativeFleetPeerSpawnReturnsToProgramE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetPeerSpawnE2ESource, "peer-spawn-program", true, 4)
}

func TestCoroNativeFleetChannelSelectCrossRouteE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetChannelSelectE2ESource, "channel-select-cross-route", true, 4)
}

func TestCoroNativeFleetShutdownCancelsPeerChannelSelectE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetChannelShutdownE2ESource, "channel-select-shutdown", true, 4)
}

func TestCoroNativeFleetSingleExecutionQuotaChannelProgressE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetSingleExecutionQuotaE2ESource, "single-quota-channel", true, 1)
}

func TestCoroNativeFleetRuntimeGOMAXPROCSQuotaE2E(t *testing.T) {
	runCoroNativeFleetE2E(t, coroNativeFleetGOMAXPROCSE2ESource, "runtime-gomaxprocs-quota", true, 1)
}

func runCoroNativeFleetE2E(t *testing.T, source, name string, enableChannel bool, initialLimit uint32) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine fleet link smoke requires Darwin or Linux")
	}
	if initialLimit == 0 {
		t.Fatal("native coroutine fleet E2E initial execution limit must be positive")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ar, err := exec.LookPath("llvm-ar")
	if err != nil {
		ar, err = exec.LookPath("ar")
		if err != nil {
			t.Skip("llvm-ar/ar is unavailable")
		}
	}

	llssa.Initialize(llssa.InitAll)
	temp := t.TempDir()
	prog := llssa.NewProgram(nil)
	prog.SetRuntime(func() *types.Package {
		rt, err := goimporter.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime type model:", err)
		}
		return rt
	})
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	defer prog.Dispose()

	userObject, anchor, setupSymbol, checkSymbol := buildCoroSpawnNativeE2EUserSource(
		t, prog, temp, source, enableChannel,
	)
	entryObject := buildCoroSpawnNativeE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroSpawnNativeE2EDriver(t, prog, temp, setupSymbol, checkSymbol)
	runtimeArchive := cachedCoroNativeFleetE2ERuntimeArchive(t, clang, ar)

	executable := filepath.Join(temp, "coro-native-fleet-"+name+"-e2e")
	// Keep an opt-in stable output path for inspecting a failed native fleet
	// executable with the platform debugger. Ordinary CI never sets it.
	if diagnostic := strings.TrimSpace(os.Getenv("LLGO_CORO_NATIVE_FLEET_OUTPUT")); diagnostic != "" {
		executable = diagnostic
	}
	linkArgs := []string{driverObject, entryObject, userObject, runtimeArchive, "-pthread", "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine fleet smoke: %v\n%s", err, output)
	}

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, executable)
	command.Env = make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GOMAXPROCS=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, fmt.Sprintf("GOMAXPROCS=%d", initialLimit))
	output, err := command.CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine fleet smoke timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine fleet smoke failed: %v\n%s", err, output)
	}
}

func cachedCoroNativeFleetE2ERuntimeArchive(t *testing.T, clang, ar string) string {
	t.Helper()
	coroNativeFleetE2ERuntimeArchiveOnce.Do(func() {
		// The runtime island and fixed C boundary are identical for every
		// program in this suite. Keep their archive under TestMain's shared
		// cache root so all E2Es pay the production-runtime compile once while
		// retaining separate user, entry, driver, link, and execution checks.
		temp := filepath.Join(cacheRootFunc(), "coro-native-fleet-e2e-runtime")
		if err := os.MkdirAll(temp, 0o755); err != nil {
			t.Fatal("create shared native fleet runtime directory:", err)
		}
		runtimeObjects := buildCoroNativeFleetE2ERuntimeIsland(t, temp)
		runtimeObjects = append(runtimeObjects, buildCoroNativeFleetE2EBoundaryObject(t, clang, temp))
		archive := filepath.Join(temp, "libllgo-coro-fleet-runtime-island.a")
		arArgs := append([]string{"rcs", archive}, runtimeObjects...)
		if output, err := exec.Command(ar, arArgs...).CombinedOutput(); err != nil {
			t.Fatalf("archive coroutine fleet runtime island: %v\n%s", err, output)
		}
		coroNativeFleetE2ERuntimeArchive = archive
	})
	if coroNativeFleetE2ERuntimeArchive == "" {
		t.Fatal("shared native fleet runtime archive is unavailable")
	}
	return coroNativeFleetE2ERuntimeArchive
}

func buildCoroNativeFleetE2EBoundaryObject(t *testing.T, clang, temp string) string {
	t.Helper()
	source := filepath.Join("testdata", "coro_native_fleet_e2e.c")
	object := filepath.Join(temp, "coro-native-fleet-e2e-boundary.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-pthread", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine fleet E2E boundary: %v\n%s", err, output)
	}
	return object
}

func buildCoroNativeFleetE2ERuntimeIsland(t *testing.T, temp string) []string {
	t.Helper()
	files := []string{
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_allocator.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_abort_libc.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_frame.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_program.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_decision.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_slice.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_execution_quota_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_sched.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor_driver_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_nil_fault.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_panic_payload.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_spawn.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_program_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_fleet_reactor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_ready_distribution_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_native_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_wait_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_timer_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_completion_fleet_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_resume_materialize.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro_atomic_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_wait_coro.go"),
	}
	files = materializeCoroChannelNativeE2ERuntimeIsland(t, files)
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	conf.compilerBuildTags = []string{
		"llgo_coro",
		coroNativePipeBuildTag,
		coroNativeTimerBuildTag,
	}
	configureCoroRuntimeIslandPlan(conf)
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/coroclock":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
		"github.com/goplus/llgo/runtime/internal/corofleet":    true,
		"github.com/goplus/llgo/runtime/internal/corotimer":    true,
		"github.com/goplus/llgo/runtime/internal/coroworker":   true,
		"github.com/goplus/llgo/runtime/internal/runtime/math": true,
	}
	seen := make(map[string]bool, len(allowed))
	var objects []string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.LPkg.Prog == nil || !allowed[pkg.ID] {
			return
		}
		if seen[pkg.ID] {
			t.Fatalf("native fleet runtime emitted duplicate module %q", pkg.ID)
		}
		seen[pkg.ID] = true
		module := pkg.LPkg.Module()
		if module.IsNil() {
			return
		}
		name := fmt.Sprintf("fleet-runtime-%03d-%s.o", len(objects), sanitizeCoroSpawnNativeE2EObjectName(pkg.ID))
		objects = append(objects, emitCoroSpawnNativeE2EObject(t, pkg.LPkg.Prog, module, filepath.Join(temp, name)))
	}
	pkgs, err := Do(files, conf)
	if err != nil {
		t.Fatalf("compile native fleet production runtime island: %v", err)
	}
	if len(pkgs) == 0 || pkgs[0].LPkg == nil {
		t.Fatal("native fleet production runtime island produced no root package")
	}
	pkgs[0].LPkg.Prog.Dispose()
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("native fleet runtime did not emit required module %q", id)
		}
	}
	objects = append(objects,
		buildCoroNativeWorkerCallObject(t, temp),
		buildCoroNativeDoorbellObject(t, temp),
		buildCoroNativeFleetOwnerObject(t, temp),
	)
	if len(objects) != len(allowed)+3 {
		t.Fatalf("native fleet runtime objects = %d, want exactly %d package objects plus worker, doorbell, and fleet-owner leaves", len(objects), len(allowed))
	}
	return objects
}
