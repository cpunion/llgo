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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLogicalCallerHelpersAreDemandedAndEmitted(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main

import "runtime"

//go:noinline
func caller() {
	runtime.Caller(0)
}

func main() {
	caller()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	helpers := []string{
		"PushCallerLocationFrame",
		"PopCallerLocationFrame",
		"UpdateLogicalCallerLocation",
	}
	legacyLocationHelpers := []string{"RecordCallerLocation", "RecordPanicLocation"}
	helperSet := make(map[string]struct{}, len(helpers))
	for _, name := range helpers {
		helperSet[name] = struct{}{}
	}
	plans := make(map[string]coro.FunctionPlan, len(helpers))
	definitions := make(map[string]bool, len(helpers)*2)
	legacyCalls := make(map[string][]string)
	logicalUpdateCalls := 0
	legacyLogicalUpdateABI := regexp.MustCompile(
		`UpdateLogicalCallerLocation"\(ptr [^,]+, i64 [^,]+, i64 `,
	)
	legacyLogicalUpdateCalls := make(map[string][]string)
	callerTokenAlloca := regexp.MustCompile(`^(%[-A-Za-z0-9._]+) = alloca \{ ptr, i64 \}, align `)
	callerTokenZero := regexp.MustCompile(`@llvm\.memset\.[^(]*\(ptr (%[-A-Za-z0-9._]+), i8 0, i64 [1-9][0-9]*, i1 false\)`)
	callerTokenPush := regexp.MustCompile(
		`PushCallerLocationFrame\$outcome"\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr (%[-A-Za-z0-9._]+),`,
	)
	callerTokenUpdate := regexp.MustCompile(
		`UpdateLogicalCallerLocation"\(ptr (%[-A-Za-z0-9._]+),`,
	)
	callerTokenFunction := regexp.MustCompile(`^define [^{]*@"([^"]+)"\(`)
	type callerTokenIR struct {
		allocas []string
		zeros   []string
		pushes  []string
		updates []string
	}
	tokenFunctions := make(map[string]*callerTokenIR)

	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		if len(plans) == len(helpers) {
			return
		}
		for _, function := range plan.Functions() {
			fn := function.Function
			if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil ||
				fn.Pkg.Pkg.Path() != llssa.PkgRuntime {
				continue
			}
			if _, wanted := helperSet[fn.Name()]; wanted {
				plans[fn.Name()] = function.Plan
			}
		}
	}
	conf.ModuleHook = func(pkg Package) {
		if pkg.PkgPath == llssa.PkgRuntime {
			for _, name := range helpers {
				for _, suffix := range []string{"", "$coro"} {
					symbol := llssa.PkgRuntime + "." + name + suffix
					fn := pkg.LPkg.FuncOf(symbol)
					definitions[symbol] = fn != nil && fn.HasBody()
				}
			}
		}
		currentFunction := ""
		for _, line := range strings.Split(pkg.LPkg.String(), "\n") {
			trimmed := strings.TrimSpace(line)
			if pkg.PkgPath == "command-line-arguments" {
				if match := callerTokenFunction.FindStringSubmatch(trimmed); len(match) == 2 {
					currentFunction = match[1]
				}
				if currentFunction != "" {
					record := tokenFunctions[currentFunction]
					if record == nil {
						record = &callerTokenIR{}
						tokenFunctions[currentFunction] = record
					}
					if match := callerTokenAlloca.FindStringSubmatch(trimmed); len(match) == 2 {
						record.allocas = append(record.allocas, match[1])
					}
					if match := callerTokenZero.FindStringSubmatch(trimmed); len(match) == 2 {
						record.zeros = append(record.zeros, match[1])
					}
					if match := callerTokenPush.FindStringSubmatch(trimmed); len(match) == 2 {
						record.pushes = append(record.pushes, match[1])
					}
					if match := callerTokenUpdate.FindStringSubmatch(trimmed); len(match) == 2 {
						record.updates = append(record.updates, match[1])
					}
				}
				if trimmed == "}" {
					currentFunction = ""
				}
			}
			if !strings.Contains(trimmed, " call ") &&
				!strings.HasPrefix(trimmed, "call ") &&
				!strings.Contains(trimmed, " invoke ") &&
				!strings.HasPrefix(trimmed, "invoke ") {
				continue
			}
			for _, name := range legacyLocationHelpers {
				symbol := llssa.PkgRuntime + "." + name
				if strings.Contains(trimmed, `@"`+symbol) {
					legacyCalls[pkg.PkgPath] = append(legacyCalls[pkg.PkgPath], trimmed)
				}
			}
			if strings.Contains(trimmed, `@"`+llssa.PkgRuntime+`.UpdateLogicalCallerLocation"(`) {
				logicalUpdateCalls++
				if legacyLogicalUpdateABI.MatchString(trimmed) {
					legacyLogicalUpdateCalls[pkg.PkgPath] = append(legacyLogicalUpdateCalls[pkg.PkgPath], trimmed)
				}
			}
		}
	}

	pkgs, err := Do([]string{"file=" + source}, conf)
	if err != nil {
		t.Fatalf("build runtime.Caller fixture: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("build runtime.Caller fixture packages = %d, want 1", len(pkgs))
	}
	for _, name := range helpers {
		plan, planned := plans[name]
		if !planned {
			t.Errorf("%s has no whole-program function plan", name)
			continue
		}
		if plan.Emission == coro.EmitNone {
			t.Errorf("%s plan = %+v, want demanded emission", name, plan)
		}
		symbol := llssa.PkgRuntime + "." + name
		switch plan.Emission {
		case coro.EmitCoroutine:
			symbol += "$coro"
		case coro.EmitPlain, coro.EmitRawPlain:
		default:
			t.Errorf("%s plan = %+v, want a defined physical body", name, plan)
			continue
		}
		if !definitions[symbol] {
			t.Errorf(
				"%s has no definition for planned symbol %q in package %q; plan=%+v",
				name, symbol, llssa.PkgRuntime, plan,
			)
		}
	}
	update := plans["UpdateLogicalCallerLocation"]
	if update.Emission != coro.EmitPlain || update.Effect != coro.NoSuspend ||
		update.Exec&(coro.NeedsPreempt|coro.MayUnwind) != 0 {
		t.Errorf("UpdateLogicalCallerLocation plan = %+v, want one infallible plain O(1) helper", update)
	}
	if definitions[llssa.PkgRuntime+".UpdateLogicalCallerLocation$coro"] {
		t.Error("UpdateLogicalCallerLocation unexpectedly emitted a coroutine body")
	}
	if logicalUpdateCalls == 0 {
		t.Error("managed runtime.Caller fixture emitted no logical source-location update")
	}
	tokenFunctionCount := 0
	for function, record := range tokenFunctions {
		if len(record.pushes) == 0 {
			continue
		}
		tokenFunctionCount++
		if len(record.allocas) != 1 || len(record.pushes) != 1 || len(record.updates) == 0 {
			t.Errorf("logical caller token IR in %q: %+v; want one alloca/push and at least one update", function, record)
			continue
		}
		token := record.allocas[0]
		if record.pushes[0] != token {
			t.Errorf("logical caller token in %q: alloca=%q push=%q", function, token, record.pushes[0])
		}
		zeroCount := 0
		for _, zero := range record.zeros {
			if zero == token {
				zeroCount++
			}
		}
		if zeroCount != 1 {
			t.Errorf("logical caller token %q in %q zeroed %d times, want 1", token, function, zeroCount)
		}
		for _, update := range record.updates {
			if update != token {
				t.Errorf("logical caller update in %q uses %q, want token %q", function, update, token)
			}
		}
	}
	if tokenFunctionCount != 2 {
		t.Errorf("logical caller token functions = %d, want caller and main", tokenFunctionCount)
	}
	for pkgPath, calls := range legacyLogicalUpdateCalls {
		t.Errorf(
			"package %q retained the non-dominating store/index update ABI:\n%s",
			pkgPath, strings.Join(calls, "\n"),
		)
	}
	for pkgPath, calls := range legacyCalls {
		if len(calls) != 0 {
			if len(calls) > 8 {
				calls = append(calls[:8], "... truncated ...")
			}
			t.Errorf(
				"package %q retained per-site native-PC logical-caller helper calls:\n%s",
				pkgPath, strings.Join(calls, "\n"),
			)
		}
	}
}
