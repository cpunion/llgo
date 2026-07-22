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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/goplus/llgo/internal/coro"
)

const coroStdlibAcceptanceEnv = "LLGO_CORO_STDLIB_ACCEPTANCE"

type coroStdlibSyncFixture struct {
	name             string
	dir              string
	wantSource       []string
	wantSchedulerABI string
	wantGo           bool
	wantChannel      bool
	requireGoStmt    bool
	args             func(*testing.T) []string
	check            func(*testing.T, time.Duration)
}

func coroStdlibSyncFixtures() []coroStdlibSyncFixture {
	return []coroStdlibSyncFixture{
		{
			// P0 timer/park probe: one ordinary synchronous Sleep and no
			// channel, callback, Stop/Reset, or multi-event select semantics.
			// The coroutine time patch also replaces AfterFunc so it does not
			// publish the unused legacy goFunc launcher into this plan.
			name:             "time",
			dir:              "./_testgo/coro_stdlib_time_sleep",
			wantSource:       []string{"time.Sleep("},
			wantSchedulerABI: coro.SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0,
			wantGo:           true,
			check: func(t *testing.T, elapsed time.Duration) {
				t.Helper()
				// The child sleeps for 200 ms. Keep enough margin for coarse host
				// clocks while still rejecting a no-op/immediate Sleep implementation.
				if elapsed < 150*time.Millisecond {
					t.Fatalf("synchronous time.Sleep returned after %s, want at least 150ms", elapsed)
				}
			},
		},
		{
			// Kept as the later full Go 1.26 timer semantics gate. It is not
			// part of the core-first P0 time,file,tcp probe set.
			name: "timer",
			dir:  "./_testgo/coro_stdlib_timer",
			wantSource: []string{
				"time.NewTimer(", ".Stop()", ".Reset(", "time.After(", "time.NewTicker(", "time.AfterFunc(",
			},
			wantSchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
			wantGo:           true,
			wantChannel:      true,
		},
		{
			// P0 regular-file worker probe: exactly one small blocking
			// Write/Read round trip, without poll deadlines or slice growth.
			// Go 1.26 sync.WaitGroup.Go remains a reflect-visible stdlib method,
			// so the complete linked method closure also requires spawn support.
			// os/io also retains io.Pipe's channel-close methods; their synchronous
			// source surface requires the channel scheduler even though this narrow
			// fixture performs only regular-file I/O.
			name:             "file",
			dir:              "./_testgo/coro_stdlib_file_rw",
			wantSource:       []string{"os.OpenFile(", "[1]byte", ".Write(", ".Seek(", ".Read(", ".Close("},
			wantSchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
			wantGo:           true,
			wantChannel:      true,
			args: func(t *testing.T) []string {
				t.Helper()
				return []string{filepath.Join(t.TempDir(), "roundtrip.txt")}
			},
		},
		{
			// Core worker probe below os.File: generated fixed-target syscall
			// wrappers keep synchronous Go call syntax while avoiding both the
			// reflect-visible *os.File method table and the unsafe dynamic-trap
			// RawSyscall surface. The higher-level file fixture remains the
			// os/reflect compatibility gate.
			name: "syscall-file",
			dir:  "./_testgo/coro_stdlib_syscall_file_rw",
			wantSource: []string{
				"syscall.Open(", "syscall.Write(", "syscall.Seek(",
				"syscall.Read(", "syscall.Close(", "syscall.Unlink(", "[1]byte",
			},
			wantSchedulerABI: coro.SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0,
			wantGo:           true,
		},
		{
			// P0 readiness/deadline probe: one top-level server G and one-byte
			// TCP operations. The fixture does not spell a channel operation, but
			// Go 1.26 net.(*netFD).connect itself uses a nonblocking channel select,
			// so the complete standard-library implementation requires Channel in
			// addition to Poll, Worker, and closed static spawn.
			name: "tcp",
			dir:  "./_testgo/coro_stdlib_tcp_loopback",
			wantSource: []string{
				"net.ListenTCP(", "net.DialTCP(", ".AcceptTCP(", "go serve()", "[1]byte",
				".Write(", ".Read(", "time.Sleep(60 * time.Millisecond)", ".SetReadDeadline(",
				"os.ErrDeadlineExceeded", "time.Time{}",
			},
			wantSchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
			wantGo:           true,
			wantChannel:      true,
			requireGoStmt:    true,
		},
	}
}

func coroStdlibSyncAcceptanceConfig(fixture coroStdlibSyncFixture, output string) *Config {
	conf := NewDefaultConf(ModeBuild)
	conf.OutFile = output
	conf.ForceRebuild = true
	conf.EnableCoroEntryResolution = true
	conf.EnableCoroPhysicalABI = true
	conf.EnableCoroChildAwait = true
	conf.EnableCoroPlainDispatch = true
	// Ordinary stdlib code contains defer/panic boundaries (notably sync.Once).
	// Managed child outcomes must therefore return through the parent's cleanup
	// path instead of using legacy native-stack unwinding.
	conf.EnableCoroExplicitStatusPanicABI = true
	conf.EnableCoroProgramBootstrapABI = true
	conf.EnableCoroProgramBootstrapRun = true
	conf.EnableCoroClosedStaticSpawn = fixture.wantGo
	conf.EnableCoroChannel = fixture.wantChannel
	conf.EnableCoroWorker = true
	conf.EnableCoroNativeFleet = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{
			DynamicResolution:    coro.DynamicCHAClosed,
			MaxPlainInstructions: -1,
		})
		if err != nil {
			return nil, err
		}
		if fixture.name == "time" || fixture.name == "timer" {
			if err := validateCoroStdlibTimePlanHasNoLegacyGoFunc(plan); err != nil {
				return nil, err
			}
		}
		return plan, nil
	}
	return conf
}

// validateCoroStdlibTimePlanHasNoLegacyGoFunc binds the source-patch contract
// to the whole-program plan. The coroutine timer manager launches the user
// callback from newTimer's arg through its managed wrapper, so time.goFunc is
// neither an executable callback nor a valid signature-wide spawn producer.
func validateCoroStdlibTimePlanHasNoLegacyGoFunc(plan *coro.SSAPlan) error {
	if plan == nil {
		return fmt.Errorf("coroutine time acceptance has no compilation plan")
	}
	for _, function := range plan.Functions() {
		fn := function.Function
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || fn.Pkg.Pkg.Path() != "time" || fn.Name() != "goFunc" {
			continue
		}
		if function.Plan.Demand != coro.NoDemand || function.Plan.Emission != coro.EmitNone {
			return fmt.Errorf(
				"coroutine time plan retained unused legacy time.goFunc (demand=%s emission=%s effect=%s)",
				function.Plan.Demand, function.Plan.Emission, function.Plan.Effect,
			)
		}
	}
	return nil
}

func TestCoroStdlibSyncAcceptanceConfiguration(t *testing.T) {
	for _, fixture := range coroStdlibSyncFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			conf := coroStdlibSyncAcceptanceConfig(fixture, filepath.Join(t.TempDir(), "acceptance"))
			if !conf.EnableCoroWorker {
				t.Fatal("synchronous stdlib acceptance must enable the native worker capability")
			}
			if !conf.EnableCoroNativeFleet {
				t.Fatal("synchronous stdlib acceptance must exercise the native multi-owner fleet")
			}
			if !conf.EnableCoroExplicitStatusPanicABI {
				t.Fatal("synchronous stdlib acceptance must propagate child panic through parent cleanup")
			}
			if got := activeCoroSchedulerABIVersion(conf); got != fixture.wantSchedulerABI {
				t.Fatalf("configured scheduler ABI = %q, want %q", got, fixture.wantSchedulerABI)
			}
		})
	}
}

func assertCoroStdlibSyncRuntimeSelection(t *testing.T, fixture coroStdlibSyncFixture, packages []Package) {
	t.Helper()
	const runtimePackage = "github.com/goplus/llgo/runtime/internal/runtime"
	required := map[string]bool{
		"coro_executor_driver_timer_llgo.go":    false,
		"coro_native_fleet.go":                  false,
		"coro_native_fleet_owner_llgo.go":       false,
		"coro_native_fleet_program_llgo.go":     false,
		"coro_native_fleet_reactor.go":          false,
		"coro_notify_owner_llgo.go":             false,
		"coro_poll_descriptor_llgo.go":          false,
		"coro_poll_owner_llgo.go":               false,
		"coro_poll_route_native_fleet_llgo.go":  false,
		"coro_ready_distribution_fleet_llgo.go": false,
		"coro_sema_owner_llgo.go":               false,
		"coro_target_native_fleet_llgo.go":      false,
		"coro_target_wait_timer_llgo.go":        false,
		"coro_timer_owner_llgo.go":              false,
		"coro_worker_completion_fleet_llgo.go":  false,
		"coro_worker_native_llgo.go":            false,
		"coro_worker_owner_llgo.go":             false,
	}
	forbidden := map[string]bool{
		"coro_executor_driver_legacy.go":         false,
		"coro_poll_route_default_llgo.go":        false,
		"coro_ready_distribution_default.go":     false,
		"coro_target_native_llgo.go":             false,
		"coro_target_none.go":                    false,
		"coro_target_test_adapter.go":            false,
		"coro_target_wait_pipe_llgo.go":          false,
		"coro_worker_completion_program_llgo.go": false,
	}
	var runtimePkg Package
	var stdlibRuntimePkg Package
	var mainPkg Package
	for _, pkg := range packages {
		if pkg == nil {
			continue
		}
		if pkg.PkgPath == runtimePackage {
			runtimePkg = pkg
		}
		if pkg.PkgPath == "runtime" {
			stdlibRuntimePkg = pkg
		}
		if pkg.Name == "main" && mainPkg == nil {
			mainPkg = pkg
		}
	}
	if runtimePkg == nil {
		t.Fatalf("%s acceptance build has no production runtime package %q", fixture.name, runtimePackage)
	}
	for _, path := range append(append([]string(nil), runtimePkg.GoFiles...), runtimePkg.CompiledGoFiles...) {
		name := filepath.Base(path)
		if _, ok := required[name]; ok {
			required[name] = true
		}
		if _, ok := forbidden[name]; ok {
			forbidden[name] = true
		}
	}
	for name, selected := range required {
		if !selected {
			t.Errorf("%s acceptance runtime did not select %s", fixture.name, name)
		}
	}
	for name, selected := range forbidden {
		if selected {
			t.Errorf("%s acceptance runtime selected incompatible %s", fixture.name, name)
		}
	}

	// The ordinary stdlib-facing runtime package must use the coroutine poll
	// and semaphore adapters. Merely retaining the owner-side symbols is not
	// enough: selecting either legacy pthread adapter would hide a blocking
	// native stack below the synchronous Go API.
	if stdlibRuntimePkg == nil || stdlibRuntimePkg.AltPkg == nil {
		t.Fatalf("%s acceptance runtime has no selected llgo runtime patch package", fixture.name)
	}
	altRequired := map[string]bool{
		"notify_coro_llgo.go":        false,
		"poll_linkname_coro_llgo.go": false,
		"sema_coro_llgo.go":          false,
		"signal_coro_llgo.go":        false,
		"time_coro_go123_llgo.go":    false,
	}
	altForbidden := map[string]bool{
		"notify_legacy_llgo.go": false,
		"poll_linkname_llgo.go": false,
		"sema_legacy_llgo.go":   false,
		"signal_llgo.go":        false,
		"time_llgo_go123.go":    false,
	}
	for _, path := range stdlibRuntimePkg.AltPkg.GoFiles {
		name := filepath.Base(path)
		if _, ok := altRequired[name]; ok {
			altRequired[name] = true
		}
		if _, ok := altForbidden[name]; ok {
			altForbidden[name] = true
		}
	}
	for name, selected := range altRequired {
		if !selected {
			t.Errorf("%s acceptance stdlib runtime did not select %s", fixture.name, name)
		}
	}
	for name, selected := range altForbidden {
		if selected {
			t.Errorf("%s acceptance stdlib runtime selected incompatible %s", fixture.name, name)
		}
	}

	if fixture.name == "time" || fixture.name == "timer" {
		const sleepPatch = "z_llgo_patch_sleep_coro_native_llgo.go"
		selected := false
		for _, pkg := range packages {
			if pkg == nil || pkg.PkgPath != "time" {
				continue
			}
			for _, path := range append(append([]string(nil), pkg.GoFiles...), pkg.CompiledGoFiles...) {
				if filepath.Base(path) == sleepPatch {
					selected = true
				}
			}
		}
		if !selected {
			t.Errorf("time acceptance did not select %s", sleepPatch)
		}
	}

	if mainPkg == nil || mainPkg.Manifest == "" {
		t.Fatalf("%s acceptance build has no main-package manifest", fixture.name)
	}
	manifest, err := decodeManifest(mainPkg.Manifest)
	if err != nil {
		t.Fatalf("decode %s acceptance manifest: %v", fixture.name, err)
	}
	if manifest.Common == nil || manifest.Common.CoroSchedulerABI != fixture.wantSchedulerABI {
		got := ""
		if manifest.Common != nil {
			got = manifest.Common.CoroSchedulerABI
		}
		t.Fatalf("%s acceptance scheduler ABI = %q, want %q", fixture.name, got, fixture.wantSchedulerABI)
	}
}

// TestCoroStdlibSyncAcceptanceFixtures is always on. It does not claim that
// the runtime capability works: it freezes the user-facing contract of the
// opt-in executable gates below. In particular, the fixtures must remain
// ordinary synchronous Go source and must not hide an explicit Future/Await
// API in a helper or llgo-private import.
func TestCoroStdlibSyncAcceptanceFixtures(t *testing.T) {
	for _, fixture := range coroStdlibSyncFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(fixture.dir, "main.go")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range fixture.wantSource {
				if !strings.Contains(string(source), want) {
					t.Errorf("%s has no required synchronous call %q", path, want)
				}
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, source, parser.AllErrors)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			goStatements := 0
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.GoStmt:
					goStatements++
				case *ast.Ident:
					if name := strings.ToLower(node.Name); name == "future" || name == "await" {
						t.Errorf("%s uses explicit async identifier %q", path, node.Name)
					}
				case *ast.SelectorExpr:
					if strings.EqualFold(node.Sel.Name, "Await") {
						t.Errorf("%s calls explicit Await", path)
					}
				}
				return true
			})
			for _, imported := range file.Imports {
				pathValue := strings.Trim(imported.Path.Value, "\"")
				if strings.Contains(pathValue, "goplus/llgo") || strings.Contains(pathValue, "llvm") {
					t.Errorf("fixture imports implementation package %q", pathValue)
				}
			}
			if fixture.requireGoStmt && goStatements == 0 {
				t.Errorf("%s fixture has no required go statement", fixture.name)
			}
			if !fixture.requireGoStmt && goStatements != 0 {
				t.Errorf("fixture has %d unexpected go statements", goStatements)
			}
		})
	}
}

// TestCoroStdlibSyncAcceptance is deliberately opt-in until the P0 time,file,tcp
// programs compile, link, and run. Use that exact comma list for the core-first
// gate. The larger timer fixture remains explicitly selectable as a later Go
// 1.26 semantics gate; "all" includes it. Once selected, a build/runtime failure
// is a real test failure and is never converted into a known-failure pass.
func TestCoroStdlibSyncAcceptance(t *testing.T) {
	selected := parseCoroStdlibAcceptanceSelection(t)
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("native coroutine acceptance runtime is unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	for _, fixture := range coroStdlibSyncFixtures() {
		fixture := fixture
		if !selected[fixture.name] {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "acceptance")
			// A stable opt-in path lets a failed native acceptance executable be
			// inspected with the platform debugger instead of disappearing with
			// testing.T's temporary directory. Ordinary CI never sets it.
			if diagnostic := strings.TrimSpace(os.Getenv("LLGO_CORO_STDLIB_OUTPUT")); diagnostic != "" {
				bin = diagnostic
			}
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			conf := coroStdlibSyncAcceptanceConfig(fixture, bin)
			if got := activeCoroSchedulerABIVersion(conf); got != fixture.wantSchedulerABI {
				t.Fatalf("%s acceptance configured scheduler ABI = %q, want %q", fixture.name, got, fixture.wantSchedulerABI)
			}

			packages, err := Do([]string{fixture.dir}, conf)
			if err != nil {
				t.Fatalf("%s synchronous stdlib acceptance build failed: %v", fixture.name, err)
			}
			assertCoroStdlibSyncRuntimeSelection(t, fixture, packages)
			var args []string
			if fixture.args != nil {
				args = fixture.args(t)
			}
			started := time.Now()
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, args...)
			output, err := cmd.CombinedOutput()
			elapsed := time.Since(started)
			if ctx.Err() == stdcontext.DeadlineExceeded {
				t.Fatalf("%s synchronous stdlib acceptance timed out after %s; output:\n%s", fixture.name, elapsed, output)
			}
			if err != nil {
				t.Fatalf("%s synchronous stdlib acceptance run failed after %s: %v; output:\n%s", fixture.name, elapsed, err, output)
			}
			if fixture.check != nil {
				fixture.check(t, elapsed)
			}
		})
	}
}

func parseCoroStdlibAcceptanceSelection(t *testing.T) map[string]bool {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(coroStdlibAcceptanceEnv))
	if raw == "" {
		t.Skipf("set %s=all or a comma-separated subset of time,timer,file,syscall-file,tcp", coroStdlibAcceptanceEnv)
	}
	known := map[string]bool{"time": true, "timer": true, "file": true, "syscall-file": true, "tcp": true}
	selected := make(map[string]bool, len(known))
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "all" {
			for name := range known {
				selected[name] = true
			}
			continue
		}
		if !known[item] {
			t.Fatalf("unknown %s selection %q; want all or time,timer,file,syscall-file,tcp", coroStdlibAcceptanceEnv, item)
		}
		selected[item] = true
	}
	if len(selected) == 0 {
		t.Fatalf("%s selected no acceptance cases", coroStdlibAcceptanceEnv)
	}
	return selected
}
