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

func TestTimeSleepWasSplitWithoutReplacingTimerTicker(t *testing.T) {
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

	if names := sourceFunctionsNamed(t, legacySleepSource, "timeSleep", "timeSleepWake"); !slices.Equal(names, []string{"timeSleep", "timeSleepWake"}) {
		t.Errorf("%s Sleep functions = %v", legacySleepSource, names)
	}
	if names := sourceFunctionsNamed(t, legacyGo123SleepSource, "timeSleep", "timeSleepWake"); !slices.Equal(names, []string{"timeSleep", "timeSleepWake"}) {
		t.Errorf("%s Sleep functions = %v", legacyGo123SleepSource, names)
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
