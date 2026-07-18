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
	name       string
	dir        string
	wantSource []string
	wantGo     bool
	args       func(*testing.T) []string
	check      func(*testing.T, time.Duration)
}

func coroStdlibSyncFixtures() []coroStdlibSyncFixture {
	return []coroStdlibSyncFixture{
		{
			name:       "time",
			dir:        "./_testgo/coro_stdlib_time_sleep",
			wantSource: []string{"time.Sleep("},
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
			name:       "file",
			dir:        "./_testgo/coro_stdlib_file_rw",
			wantSource: []string{"os.OpenFile(", ".Write(", ".Read("},
			args: func(t *testing.T) []string {
				t.Helper()
				return []string{filepath.Join(t.TempDir(), "roundtrip.txt")}
			},
		},
		{
			name:       "tcp",
			dir:        "./_testgo/coro_stdlib_tcp_loopback",
			wantSource: []string{"net.ListenTCP(", "net.DialTCP(", ".AcceptTCP(", ".Write(", ".Read("},
			wantGo:     true,
		},
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
			if fixture.wantGo && goStatements == 0 {
				t.Error("TCP fixture has no concurrent server go statement")
			}
			if !fixture.wantGo && goStatements != 0 {
				t.Errorf("fixture has %d unexpected go statements", goStatements)
			}
		})
	}
}

// TestCoroStdlibSyncAcceptance is deliberately opt-in until all three programs
// compile, link, and run. Set LLGO_CORO_STDLIB_ACCEPTANCE=all, or a comma list
// such as time,file,tcp. Once selected, a build/runtime failure is a real test
// failure; this gate never converts a known implementation blocker into a pass.
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
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			conf := NewDefaultConf(ModeBuild)
			conf.OutFile = bin
			conf.ForceRebuild = true
			conf.EnableCoroEntryResolution = true
			conf.EnableCoroPhysicalABI = true
			conf.EnableCoroChildAwait = true
			conf.EnableCoroPlainDispatch = true
			conf.EnableCoroProgramBootstrapABI = true
			conf.EnableCoroProgramBootstrapRun = true
			conf.EnableCoroClosedStaticSpawn = fixture.wantGo
			conf.EnableCoroChannel = fixture.wantGo
			conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
			}

			if _, err := Do([]string{fixture.dir}, conf); err != nil {
				t.Fatalf("%s synchronous stdlib acceptance build failed: %v", fixture.name, err)
			}
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
		t.Skipf("set %s=all or a comma-separated subset of time,file,tcp", coroStdlibAcceptanceEnv)
	}
	known := map[string]bool{"time": true, "file": true, "tcp": true}
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
			t.Fatalf("unknown %s selection %q; want all or time,file,tcp", coroStdlibAcceptanceEnv, item)
		}
		selected[item] = true
	}
	if len(selected) == 0 {
		t.Fatalf("%s selected no acceptance cases", coroStdlibAcceptanceEnv)
	}
	return selected
}
