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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
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
		"RecordCallerLocation",
		"RecordPanicLocation",
	}
	helperSet := make(map[string]struct{}, len(helpers))
	for _, name := range helpers {
		helperSet[name] = struct{}{}
	}
	plans := make(map[string]coro.FunctionPlan, len(helpers))
	definitions := make(map[string]bool, len(helpers)*2)
	legacyCalls := make(map[string][]string)

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
		for _, line := range strings.Split(pkg.LPkg.String(), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, " call ") &&
				!strings.HasPrefix(trimmed, "call ") &&
				!strings.Contains(trimmed, " invoke ") &&
				!strings.HasPrefix(trimmed, "invoke ") {
				continue
			}
			for _, name := range helpers {
				symbol := llssa.PkgRuntime + "." + name
				if strings.Contains(trimmed, `@"`+symbol+`"(`) {
					legacyCalls[pkg.PkgPath] = append(legacyCalls[pkg.PkgPath], trimmed)
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
	for pkgPath, calls := range legacyCalls {
		if len(calls) != 0 {
			if len(calls) > 8 {
				calls = append(calls[:8], "... truncated ...")
			}
			t.Errorf(
				"package %q retained legacy synchronous logical-caller helper calls:\n%s",
				pkgPath, strings.Join(calls, "\n"),
			)
		}
	}
}
