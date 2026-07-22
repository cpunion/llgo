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
		"func llgoCoroControlledTimerWaitV2(controller unsafe.Pointer, control *uint32, expected uint32, deadline int64) uint32",
		"__llgo_coro_timer_cancel_controlled_v2",
		"go coroTimerManager(t)",
		"go coroRunTimerCallback(arg.(func()))",
		"corort.MarkTimerChannel((*corort.Chan)(cp))",
		"coroTimerLock(&state.sendLock)",
		"coroTimerDrainChannel(state.channel)",
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
		"func __llgo_coro_timer_cancel_controlled_v2(",
		"coro.PrepareCurrentExecutorControlledTimerPark(",
		"coro.CancelExecutorControlledTimerV2(",
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
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("controlled timer owner contains forbidden driver behavior %q", forbidden)
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
		"type CoroTimerParkV2 struct {",
		"func __llgo_coro_timer_park_v2(",
		"func __llgo_coro_timer_resume_v2(",
		"coro.CurrentExecutorTimerDriver(task)",
		"coro.PrepareCurrentExecutorTimerPark(",
		"coro.TakeRunDecision(task, state.ticket)",
		"coro.FinishCurrentExecutorTimerPark(",
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
}

func TestNativeTimerCapacityIsIndependentAndAdmitsStandardLibraryStress(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("internal", "runtime", "coro_executor_driver_timer_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, contract := range []string{
		"coroNativeSourcePageCountV1 = 16",
		"coroNativeTimerPageCountV1  = 64",
		"coroNativeTimerCapacityV1   = coroNativeTimerPageCountV1 * coro.TimerRegistrationPageCapacity",
		"coroProgramTimerExtraPagesV1State   [coroNativeTimerPageCountV1 - 1]coro.TimerRegistrationPage",
		"coroProgramPollExtraPagesV1State    [coroNativeSourcePageCountV1 - 1]coro.PollOperationPage",
		"coroProgramManualExtraPagesV2State  [coroNativeManualPageCountV2 - 1]coro.ManualOperationPage",
		"coroProgramWorkerExtraPagesV1State  [coroNativeSourcePageCountV1 - 1]coro.WorkerOperationPage",
		"coroProgramChannelExtraPagesV1State [coroNativeSourcePageCountV1 - 1]coro.ChannelOperationPage",
		"coro.TimerRegistrationConfiguredCapacity(&coroProgramTimerTableV1State) != coroNativeTimerCapacityV1",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("native timer capacity source lacks %q", contract)
		}
	}
	if strings.Contains(source, "coroNativeTimerCapacityV1 = coroNativeSourcePageCountV1") ||
		strings.Contains(source, "coroProgramTimerExtraPagesV1State [coroNativeSourcePageCountV1 - 1]") {
		t.Error("native timer storage is still coupled to the 1024-entry common-source page count")
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
