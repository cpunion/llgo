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

package runtime

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	timeSleepSourceDir     = "internal/lib/runtime"
	legacySleepSource      = "time_sleep_legacy_llgo.go"
	legacyGo123SleepSource = "time_sleep_legacy_go123_llgo.go"
	legacyTimerSource      = "time_llgo.go"
	legacyGo123TimerSource = "time_llgo_go123.go"
	coroGo123TimerSource   = "time_coro_go123_llgo.go"
	timeSleepLinkname      = "//go:linkname timeSleep time.Sleep"
)

func TestTimeSleepSourceSelection(t *testing.T) {
	nativeTags := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name        string
		goos        string
		buildTags   []string
		beforeGo123 bool
		want        string
	}{
		{name: "ordinary go1.23 or newer", goos: "linux", want: legacyGo123SleepSource},
		{name: "ordinary before go1.23", goos: "linux", beforeGo123: true, want: legacySleepSource},
		{name: "native coroutine linux uses time source patch", goos: "linux", buildTags: nativeTags},
		{name: "native coroutine darwin uses time source patch", goos: "darwin", buildTags: nativeTags},
		{name: "native capability incomplete falls back", goos: "linux", buildTags: []string{"llgo_coro_native_timer"}, want: legacyGo123SleepSource},
		{name: "native adapter falls back", goos: "linux", buildTags: append(slices.Clone(nativeTags), "coro_runtime_adapter_test"), want: legacyGo123SleepSource},
		{name: "native windows falls back", goos: "windows", buildTags: nativeTags, want: legacyGo123SleepSource},
		{name: "baremetal owns sleep elsewhere", goos: "linux", buildTags: []string{"baremetal"}},
	}
	files := []string{legacySleepSource, legacyGo123SleepSource}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "amd64"
			ctx.BuildTags = slices.Clone(test.buildTags)
			if test.beforeGo123 {
				ctx.ReleaseTags = releaseTagsBeforeGo123(ctx.ReleaseTags)
			}
			for _, file := range files {
				got, err := ctx.MatchFile(timeSleepSourceDir, file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if got != (file == test.want) {
					t.Errorf("MatchFile(%q) = %t, want %t", file, got, file == test.want)
				}
			}
		})
	}
}

func TestTimeTimerSourceSelection(t *testing.T) {
	nativeTags := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name      string
		goos      string
		buildTags []string
		want      string
	}{
		{name: "ordinary go1.23 or newer", goos: "linux", want: legacyGo123TimerSource},
		{name: "full coroutine linux", goos: "linux", buildTags: nativeTags, want: coroGo123TimerSource},
		{name: "full coroutine darwin", goos: "darwin", buildTags: nativeTags, want: coroGo123TimerSource},
		{name: "missing pipe falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_timer"}, want: legacyGo123TimerSource},
		{name: "adapter test falls back", goos: "linux", buildTags: append(slices.Clone(nativeTags), "coro_runtime_adapter_test"), want: legacyGo123TimerSource},
		{name: "unsupported os falls back", goos: "windows", buildTags: nativeTags, want: legacyGo123TimerSource},
	}
	files := []string{legacyGo123TimerSource, coroGo123TimerSource}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "amd64"
			ctx.BuildTags = slices.Clone(test.buildTags)
			for _, file := range files {
				got, err := ctx.MatchFile(timeSleepSourceDir, file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if got != (file == test.want) {
					t.Errorf("MatchFile(%q) = %t, want %t", file, got, file == test.want)
				}
			}
		})
	}
}

func TestBaremetalCoroTimerBridgeSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		tags []string
		coro bool
	}{
		{"host reactor", []string{"baremetal", "llgo", "llgo_coro"}, true},
		{"host reactor with inherited native tags", []string{"baremetal", "llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}, true},
		{"adapter", []string{"baremetal", "llgo", "llgo_coro", "coro_runtime_adapter_test"}, false},
		{"legacy smoke", []string{"baremetal"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH = "linux", "arm"
			ctx.BuildTags = test.tags
			for file, want := range map[string]bool{
				"time_baremetal_llgo.go":       !test.coro,
				coroGo123TimerSource:           test.coro,
				"nanotime_llgo.go":             test.coro,
				"nanotime_host_llgo.go":        test.coro,
				"realtime_coro_host_llgo.go":   test.coro,
				"nanotime_linux_llgo.go":       false,
				"realtime_coro_native_llgo.go": false,
				legacyGo123TimerSource:         false,
				legacyGo123SleepSource:         false,
			} {
				selected, err := ctx.MatchFile(timeSleepSourceDir, file)
				if err != nil || selected != want {
					t.Errorf("%s selected=%t, want=%t: %v", file, selected, want, err)
				}
			}
			selected, err := ctx.MatchFile("_patch/time", "sleep_coro_native_llgo.go")
			if err != nil || selected != test.coro {
				t.Fatalf("standard Sleep/Timer patch selected=%t, want=%t: %v", selected, test.coro, err)
			}
		})
	}
}

func TestExplicitHostNanotimeUsesExecutorClock(t *testing.T) {
	for goos, native := range map[string]string{
		"darwin": "darwin", "linux": "linux", "windows": "windows", "freebsd": "other",
	} {
		for _, host := range []bool{false, true} {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH = goos, "arm64"
			ctx.BuildTags = []string{"llgo", "llgo_coro"}
			if host {
				ctx.BuildTags = append(ctx.BuildTags, "llgo_coro_host")
			}
			for _, source := range []string{"host", "darwin", "linux", "windows", "other"} {
				want := host && source == "host" || !host && source == native
				selected, err := ctx.MatchFile(timeSleepSourceDir, "nanotime_"+source+"_llgo.go")
				if err != nil || selected != want {
					t.Errorf("%s host=%t clock=%s selected=%t want=%t: %v", goos, host, source, selected, want, err)
				}
			}
		}
	}
}

func TestBaremetalCoroUsesFreestandingDiagnosticBoundary(t *testing.T) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH = "linux", "arm"
	ctx.BuildTags = []string{"llgo", "llgo_coro", "baremetal"}
	for file, want := range map[string]bool{
		"z_print_freestanding_webassembly.go":         true,
		"coro_abort_freestanding_webassembly.go":      true,
		"panic_traceback_freestanding_webassembly.go": true,
		"z_print_write_default.go":                    false,
		"z_print.go":                                  false,
		"z_print_format.go":                           false,
		"z_print_go126.go":                            false,
		"z_print_legacy.go":                           false,
		"coro_abort_libc.go":                          false,
		"coro_panic_report_libc.go":                   false,
		"panic_traceback_coro.go":                     false,
	} {
		selected, err := ctx.MatchFile("internal/runtime", file)
		if err != nil || selected != want {
			t.Errorf("baremetal diagnostic %s selected=%t want=%t: %v", file, selected, want, err)
		}
	}
}

func TestNanotimeUsesFrameSafeFixedLocal(t *testing.T) {
	for _, file := range []string{"nanotime_linux_llgo.go", "nanotime_other_llgo.go"} {
		source := readTimeSleepSource(t, file)
		for _, required := range []string{"var value ct.Timespec", "&value"} {
			if !strings.Contains(source, required) {
				t.Errorf("%s lacks frame-safe nanotime marker %q", file, required)
			}
		}
		for _, forbidden := range []string{"c.Alloca(", "unsafe.Sizeof("} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains coroutine-unsafe fixed local %q", file, forbidden)
			}
		}
	}
}

func TestTimeSleepAndTimerImplementationsStayProfileLocal(t *testing.T) {
	for _, file := range []string{legacyTimerSource, legacyGo123TimerSource} {
		if names := sourceFunctionsNamed(t, file, "timeSleep", "timeSleepWake"); len(names) != 0 {
			t.Errorf("%s still defines split Sleep functions: %v", file, names)
		}
		source := readTimeSleepSource(t, file)
		for _, retained := range []string{"runtimeTimer", "resetRuntimeTimer", "libuv"} {
			if !strings.Contains(source, retained) {
				t.Errorf("%s no longer contains retained Timer/Ticker implementation marker %q", file, retained)
			}
		}
	}
	coroSource := readTimeSleepSource(t, coroGo123TimerSource)
	for _, retained := range []string{"coroTimerManager", "llgoCoroTimerNewV1", "llgoCoroTimerStopV1", "llgoCoroTimerResetV1", "llgo.coroControlledTimerWait"} {
		if !strings.Contains(coroSource, retained) {
			t.Errorf("%s lacks controlled Timer/Ticker marker %q", coroGo123TimerSource, retained)
		}
	}
	for _, forbidden := range []string{"libuv", "pthread", "runtimeTimer", "time.goFunc"} {
		if strings.Contains(coroSource, forbidden) {
			t.Errorf("%s retains forbidden legacy timer dependency %q", coroGo123TimerSource, forbidden)
		}
	}
	for _, contract := range []string{
		"//go:linkname llgoCoroTimerNewV1 runtime.llgoCoroTimerNewV1",
		"func llgoCoroTimerNewV1(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer",
		"//go:linkname llgoCoroTimerStopV1 runtime.llgoCoroTimerStopV1",
		"func llgoCoroTimerStopV1(timer unsafe.Pointer) bool",
		"//go:linkname llgoCoroTimerResetV1 runtime.llgoCoroTimerResetV1",
		"func llgoCoroTimerResetV1(timer unsafe.Pointer, when, period int64) bool",
		"func llgoCoroControlledTimerWaitV2(",
		"control, ownerRoute *uint32",
		"__llgo_coro_timer_request_controlled_v2",
		"go coroTimerManager(state)",
		"state.manager = 0",
		"coroRunTimerCallback(arg.(func()))",
		"corort.MarkTimerChannel((*corort.Chan)(cp))",
		"coroTimerLock(&state.sendLock)",
		"coroTimerDrainChannel(t.c)",
		"coroTimerNextTickerDeadline(when, period, now)",
	} {
		if !strings.Contains(coroSource, contract) {
			t.Errorf("%s lacks standard timer contract %q", coroGo123TimerSource, contract)
		}
	}
	for _, obsolete := range []string{
		"//go:linkname newTimer time.newTimer",
		"//go:linkname stopTimer time.stopTimer",
		"//go:linkname resetTimer time.resetTimer",
		"llgo.coroPark",
		"__llgo_coro_timer_prepare_controlled_or_abort_v1",
		"__llgo_coro_timer_cancel_controlled_v1",
		"__llgo_coro_timer_retire_controlled_or_abort_v1",
		"__llgo_coro_timer_cancel_controlled_v2",
		"go coroRunTimerCallback(",
	} {
		if strings.Contains(coroSource, obsolete) {
			t.Errorf("%s still claims standard-library physical symbol %q", coroGo123TimerSource, obsolete)
		}
	}

	if names := sourceFunctionsNamed(t, legacySleepSource, "timeSleep", "timeSleepWake"); !slices.Equal(names, []string{"timeSleep", "timeSleepWake"}) {
		t.Errorf("%s Sleep functions = %v", legacySleepSource, names)
	}
	if names := sourceFunctionsNamed(t, legacyGo123SleepSource, "timeSleep", "timeSleepWake"); !slices.Equal(names, []string{"timeSleep", "timeSleepWake"}) {
		t.Errorf("%s Sleep functions = %v", legacyGo123SleepSource, names)
	}
}

func TestControlledTimerOwnerUsesUnifiedTimerSource(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_timer_owner_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, contract := range []string{
		"func __llgo_coro_timer_park_controlled_v2(",
		"func __llgo_coro_timer_request_controlled_v2(",
		"coro.CurrentExecutorTimerSource(task)",
		"ensureCoroTimerSourceCapacityV1(",
		"coro.PrepareSingleControlledTimerParkReserved(",
		"coroTargetRequestControlledTimerV2(",
		"catomic.Store(ownerRoute, uint32(wantRoute))",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("controlled timer owner lacks %q", contract)
		}
	}
	for _, forbidden := range []string{
		"libuv",
		"pthread",
		"go func",
		"__llgo_coro_timer_prepare_controlled_or_abort_v1",
		"__llgo_coro_timer_cancel_controlled_v1",
		"__llgo_coro_timer_retire_controlled_or_abort_v1",
		"__llgo_coro_timer_cancel_controlled_v2",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("controlled timer owner contains forbidden driver behavior %q", forbidden)
		}
	}

	targetData, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_target_native_fleet_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	targetSource := string(targetData)
	for _, contract := range []string{
		"func coroTargetRequestControlledTimerV2(route coro.RouteID) bool",
		"domain.ingress.Enter()",
		"fleet.RequestTimerExecutor(route)",
		"coroNativeFleetFinishExecutorRequestV1(domain, result)",
		"domain.ingress.Leave()",
	} {
		if !strings.Contains(targetSource, contract) {
			t.Errorf("controlled timer target route lacks %q", contract)
		}
	}
}

func TestSleepTimerV2OwnerUsesExactCurrentSource(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_timer_owner_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, contract := range []string{
		"type CoroSleepParkV2 struct {",
		"type CoroTimerParkV2 struct {",
		"func __llgo_coro_timer_park_v2(",
		"func __llgo_coro_timer_resume_v2(",
		"coro.CurrentExecutorTimerSource(task)",
		"ensureCoroTimerSourceCapacityV1(",
		"coro.PrepareSingleTimerParkReserved(",
		"coro.BindSingleOwnerTimerResume(",
		"coro.TakeSingleOwnerTimerResume(",
		"coro.BindSingleWaitSetResumePacket(",
		"coro.TakeResumePacket(",
		"resume coro.SingleOwnerTimerResume",
		"packet    coro.ResumePacket",
		"coroTimerResumeTaskAbortV2",
		"coroTimerResumeShutdownV2",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("Sleep Timer V2 owner lacks %q", contract)
		}
	}
	for _, forbidden := range []string{"libuv", "pthread", "go func"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("Sleep Timer V2 owner contains forbidden driver behavior %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		"coro.TakeRunDecision(",
		"coro.FinishCurrentExecutorTimerPark(",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("Sleep Timer V2 owner retained owner-affine resume %q", forbidden)
		}
	}
}

func TestNativeTimerCapacityIsDemandPagedAndAdmitsStandardLibraryStress(t *testing.T) {
	policyData, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_native_fleet.go"))
	if err != nil {
		t.Fatal(err)
	}
	policy := string(policyData)
	for _, contract := range []string{
		"coroNativePollLogicalCapacityV1 = 1024",
		"not eager reservations",
		"coroNativeFleetValidateOwnedDomainSourcesV1(domain)",
		"coro.TimerRegistrationConfiguredCapacity(&domain.timers) == coro.TimerRegistrationPageCapacity",
	} {
		if !strings.Contains(policy, contract) {
			t.Errorf("native fleet capacity policy lacks %q", contract)
		}
	}

	data, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_executor_driver_timer_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, contract := range []string{
		"coroNativeTimerCapacityV1 = coroRuntimeTimerCapacityV1",
		"coroNativePollCapacityV1  = coroRuntimePollCapacityV1",
		"coro.TimerRegistrationConfiguredCapacity(&coroProgramTimerTableV1State) != coro.TimerRegistrationPageCapacity",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("native timer capacity source lacks %q", contract)
		}
	}
	for _, forbidden := range []string{"coroProgramTimerExtraPagesV1State", "coroProgramPollExtraPagesV1State", "coroProgramManualExtraPagesV2State", "coroProgramChannelExtraPagesV1State", "timerPages   [", "pollPages    ["} {
		if strings.Contains(source, forbidden) || strings.Contains(policy, forbidden) {
			t.Errorf("native timer profile eagerly reserves source storage through %q", forbidden)
		}
	}
	capacityData, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_operation_capacity.go"))
	if err != nil {
		t.Fatal(err)
	}
	capacity := strings.Join(strings.Fields(string(capacityData)), " ")
	for _, contract := range []string{
		"coroRuntimeTimerCapacityV1  = coro.TimerRegistrationMaximumCapacity",
		"coroRuntimePollCapacityV1 = 1024",
		"coroRuntimeManualCapacityV1 = 2048",
		"coroRuntimeWorkerCapacityV1 = 1024",
		"func ensureCoroTimerOperationCapacityV1(",
		"page := new(coro.TimerRegistrationPage)",
		"coro.AttachTimerRegistrationPage(timers, p, page, nil)",
		"block := new(coro.OperationPageDirectoryBlock)",
	} {
		if !strings.Contains(capacity, strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("demand-paged timer capacity lacks %q", contract)
		}
	}
	timerOwner, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_timer_owner_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(timerOwner), "reservation, capacityOK := ensureCoroTimerSourceCapacityV1(") {
		t.Error("timer park owner does not demand-grow before its ParkSet transaction")
	}
}

func TestTimerChannelHasPrivateSynchronousView(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("internal", "runtime", "z_chan.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, contract := range []string{
		"timerSync bool",
		"func MarkTimerChannel(p *Chan) bool",
		"if !p.timerSync {",
		"if p.timerSync {",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("timer channel source lacks %q", contract)
		}
	}
}

func releaseTagsBeforeGo123(tags []string) []string {
	trimmed := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == "go1.23" {
			break
		}
		trimmed = append(trimmed, tag)
	}
	return trimmed
}

func readTimeSleepSource(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(timeSleepSourceDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func parseTimeSleepSource(t *testing.T, name string) *ast.File {
	t.Helper()
	path := filepath.Join(timeSleepSourceDir, name)
	file, err := parser.ParseFile(token.NewFileSet(), path, readTimeSleepSource(t, name), parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile(%q): %v", name, err)
	}
	return file
}

func sourceFunctionsNamed(t *testing.T, name string, wanted ...string) []string {
	t.Helper()
	want := make(map[string]bool, len(wanted))
	for _, name := range wanted {
		want[name] = true
	}
	var found []string
	for _, decl := range parseTimeSleepSource(t, name).Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && want[fn.Name.Name] {
			found = append(found, fn.Name.Name)
		}
	}
	return found
}
