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
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// coroArchitectureDebtBudget is an exact, deliberately monotonic snapshot. A
// replacement cohort lowers one or more fields in the same commit that deletes
// its old production consumer. Raising a value is an architecture regression,
// not an ordinary golden update. Exact equality prevents a completed cohort
// from leaving headroom in which an old path could later grow back. The final
// hard cutover keeps this test with zero budgets for legacyWait, nativeFork, and
// stagedFeatureGate; currentCoro is confined to the unified emitter and
// planAuthority to the ProgramIR builder.
type coroArchitectureDebtBudget struct {
	currentCoro       int
	planAuthority     int
	stagedFeatureGate int
	legacyWait        int
	nativeFork        int
	fleetBuildFiles   int
}

var currentCoroArchitectureDebtBudget = coroArchitectureDebtBudget{
	// Filled from the 2026-07-22 executable fleet checkpoint. These values may
	// only decrease; see TestCoroArchitectureDebtIsMonotonic.
	currentCoro:       187,
	planAuthority:     412,
	stagedFeatureGate: 330,
	legacyWait:        72,
	nativeFork:        378,
	fleetBuildFiles:   13,
}

var allowedCurrentCoroFiles = map[string]bool{
	"cl/compile.go":                true,
	"cl/coro_abi.go":               true,
	"cl/coro_await.go":             true,
	"cl/coro_channel.go":           true,
	"cl/coro_critical_lowering.go": true,
	"cl/coro_defer.go":             true,
	"cl/coro_dispatch.go":          true,
	"cl/coro_dynamic_await.go":     true,
	"cl/coro_implicit_fault.go":    true,
	"cl/coro_interface_await.go":   true,
	"cl/coro_lowered_call.go":      true,
	"cl/coro_managed_interface.go": true,
	"cl/coro_panic.go":             true,
	"cl/coro_patch_init.go":        true,
	"cl/coro_poll_wait.go":         true,
	"cl/coro_recover.go":           true,
	"cl/coro_slice_to_array.go":    true,
	"cl/coro_spawn.go":             true,
	"cl/coro_timer_sleep.go":       true,
	"cl/coro_unsafe_slice.go":      true,
	"cl/coro_unsafe_string.go":     true,
	"cl/coro_worker.go":            true,
	"cl/coro_worker_foreign.go":    true,
	"cl/instr.go":                  true,
}

var allowedStagedCoroFeatureNames = map[string]bool{
	"EnableCoroChannel":                true,
	"EnableCoroChildAwait":             true,
	"EnableCoroClosedStaticSpawn":      true,
	"EnableCoroEntryResolution":        true,
	"EnableCoroExplicitStatusPanicABI": true,
	"EnableCoroNativeFleet":            true,
	"EnableCoroPhysicalABI":            true,
	"EnableCoroPlainDispatch":          true,
	"EnableCoroProgramBootstrapABI":    true,
	"EnableCoroProgramBootstrapRun":    true,
	"EnableCoroWorker":                 true,
}

var allowedExecutorSourceCatalogFields = map[string]bool{
	"Waits": true, "Timers": true, "Poll": true, "Manual": true,
	"Worker": true, "Channel": true, "Control": true,
}

type coroArchitectureDebtInventory struct {
	coroArchitectureDebtBudget
	currentCoroFiles map[string]bool
	featureNames     map[string]bool
	sourceFields     map[string]bool
}

func TestCoroArchitectureDebtIsMonotonic(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	inventory := inspectCoroArchitectureDebt(t, repoRoot)
	budget := currentCoroArchitectureDebtBudget

	check := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("coroutine architecture debt %s = %d, snapshot %d; a replacement cohort must update code and lower this snapshot together, while raising it is forbidden", name, got, want)
		}
	}
	check("currentCoro", inventory.currentCoro, budget.currentCoro)
	check("direct plan authority", inventory.planAuthority, budget.planAuthority)
	check("staged feature gates", inventory.stagedFeatureGate, budget.stagedFeatureGate)
	check("legacy WaitToken", inventory.legacyWait, budget.legacyWait)
	check("single-P/fleet fork", inventory.nativeFork, budget.nativeFork)
	check("fleet build-constraint files", inventory.fleetBuildFiles, budget.fleetBuildFiles)

	checkExactCoroArchitectureSet(t, "currentCoro production files", inventory.currentCoroFiles, allowedCurrentCoroFiles)
	checkExactCoroArchitectureSet(t, "staged coroutine feature names", inventory.featureNames, allowedStagedCoroFeatureNames)
	checkExactCoroArchitectureSet(t, "ExecutorSourceCatalog fields", inventory.sourceFields, allowedExecutorSourceCatalogFields)
}

func checkExactCoroArchitectureSet(t *testing.T, name string, got, want map[string]bool) {
	t.Helper()
	if strings.Join(sortedCoroArchitectureKeys(got), "\x00") == strings.Join(sortedCoroArchitectureKeys(want), "\x00") {
		return
	}
	t.Errorf("%s changed:\n got %v\nwant %v; replacement cohorts must shrink this snapshot in the same commit, and additions are forbidden", name, sortedCoroArchitectureKeys(got), sortedCoroArchitectureKeys(want))
}

func inspectCoroArchitectureDebt(t *testing.T, repoRoot string) coroArchitectureDebtInventory {
	t.Helper()
	inventory := coroArchitectureDebtInventory{
		currentCoroFiles: make(map[string]bool),
		featureNames:     make(map[string]bool),
		sourceFields:     make(map[string]bool),
	}
	roots := []string{"cl", "internal/coro", "internal/build", "ssa", "runtime/internal/coro", "runtime/internal/runtime"}
	for _, root := range roots {
		walkRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		err := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			if hasCoroNativeFleetBuildConstraint(file) {
				inventory.fleetBuildFiles++
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.Ident:
					name := node.Name
					switch name {
					case "currentCoro":
						inventory.currentCoro++
						inventory.currentCoroFiles[rel] = true
					case "CoroPlan", "EmissionUniverse":
						inventory.planAuthority++
					case "WaitToken":
						inventory.legacyWait++
					case "timerRegistrationModeV1", "pollOperationModeV1", "coroNativeTargetV1State":
						inventory.nativeFork++
					default:
						if strings.HasPrefix(name, "EnableCoro") {
							inventory.stagedFeatureGate++
							inventory.featureNames[name] = true
						}
						if strings.HasPrefix(name, "coroProgram") && strings.HasSuffix(name, "V1State") {
							inventory.nativeFork++
						}
					}
				case *ast.TypeSpec:
					if node.Name.Name == "ExecutorSourceCatalog" {
						if structure, ok := node.Type.(*ast.StructType); ok {
							for _, field := range structure.Fields.List {
								for _, name := range field.Names {
									inventory.sourceFields[name.Name] = true
								}
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("inspect coroutine architecture debt under %s: %v", root, err)
		}
	}
	return inventory
}

func hasCoroNativeFleetBuildConstraint(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			text := strings.TrimSpace(comment.Text)
			if (strings.HasPrefix(text, "//go:build") || strings.HasPrefix(text, "// +build")) &&
				strings.Contains(text, "llgo_coro_native_fleet") {
				return true
			}
		}
	}
	return false
}

func sortedCoroArchitectureKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
