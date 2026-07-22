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
	currentCoro               int
	planAuthority             int
	stagedFeatureGate         int
	legacyWait                int
	nativeFork                int
	fleetBuildFiles           int
	rawHelperPlan             int
	rawHelperPhysicalType     int
	legacyHelperScan          int
	siteEmissionEntry         int
	relocatedEntry            int
	helperObservation         int
	programIRBuilderAuthority int
	rawNoInitPlan             int
	rawIntrinsicPlan          int
	rawIntrinsicOpcode        int
	rawIntrinsicShape         int
	rawWorkerCertificateStore int
	rawWorkerGraphStore       int
	rawPatchRedirectStore     int
	patchRedirectLookup       int
	legacyIntrinsicInput      int
	callSiteFreeze            int
	intrinsicObservation      int
	elisionObservation        int
	elisionSelection          int
	intrinsicSelection        int
	physicalPlanBuild         int
	physicalPlanFreeze        int
	physicalPlanCommit        int
	physicalPlanLookup        int
	physicalRecipeSelection   int
	physicalRecipeObservation int
	physicalGuardObservation  int
	physicalCodegenRebuild    int
	physicalProofBuilderCall  int
	legacyPhysicalSelector    int
	legacySplitEmissionState  int
	emissionSessionAccess     int
	bodyCapabilityAccess      int
	emissionSessionBegin      int
	emissionBodyBind          int
	emissionBodyComplete      int
	legacyContextState        int
	contextSessionField       int
	parkProtocolTemplate      int
	parkProtocolEmission      int
	legacyParkProtocolStep    int
	rawLocalBodyScan          int
	localBodyFactAuthority    int
	semanticRecipePlan        int
	semanticRecipeObservation int
	controlPlan               int
	controlSelect             int
	controlObserve            int
	operationPlan             int
	operationSelect           int
	operationObserve          int
	outcomePlan               int
	outcomeSelect             int
	outcomeObserve            int
}

var currentCoroArchitectureDebtBudget = coroArchitectureDebtBudget{
	// Filled from the 2026-07-22 executable fleet checkpoint. These values may
	// only decrease; see TestCoroArchitectureDebtIsMonotonic.
	currentCoro:               0,
	planAuthority:             367,
	stagedFeatureGate:         0,
	legacyWait:                72,
	nativeFork:                378,
	fleetBuildFiles:           13,
	rawHelperPlan:             4,
	rawHelperPhysicalType:     0,
	legacyHelperScan:          0,
	siteEmissionEntry:         1,
	relocatedEntry:            2,
	helperObservation:         3,
	programIRBuilderAuthority: 3,
	rawNoInitPlan:             2,
	rawIntrinsicPlan:          2,
	rawIntrinsicOpcode:        7,
	rawIntrinsicShape:         20,
	rawWorkerCertificateStore: 5,
	rawWorkerGraphStore:       10,
	rawPatchRedirectStore:     6,
	patchRedirectLookup:       3,
	legacyIntrinsicInput:      0,
	callSiteFreeze:            1,
	intrinsicObservation:      3,
	elisionObservation:        4,
	elisionSelection:          2,
	intrinsicSelection:        2,
	physicalPlanBuild:         2,
	physicalPlanFreeze:        1,
	physicalPlanCommit:        1,
	physicalPlanLookup:        2,
	physicalRecipeSelection:   10,
	physicalRecipeObservation: 14,
	physicalGuardObservation:  10,
	physicalCodegenRebuild:    0,
	physicalProofBuilderCall:  8,
	legacyPhysicalSelector:    0,
	legacySplitEmissionState:  0,
	emissionSessionAccess:     22,
	bodyCapabilityAccess:      42,
	emissionSessionBegin:      1,
	emissionBodyBind:          1,
	emissionBodyComplete:      1,
	legacyContextState:        0,
	contextSessionField:       1,
	parkProtocolTemplate:      1,
	parkProtocolEmission:      7,
	legacyParkProtocolStep:    0,
	rawLocalBodyScan:          2,
	localBodyFactAuthority:    2,
	semanticRecipePlan:        3,
	semanticRecipeObservation: 2,
	controlPlan:               2,
	controlSelect:             3,
	controlObserve:            4,
	operationPlan:             2,
	operationSelect:           6,
	operationObserve:          8,
	outcomePlan:               2,
	outcomeSelect:             6,
	outcomeObserve:            6,
}

var allowedCurrentCoroFiles = map[string]bool{}

var allowedEmissionSessionAccessFiles = map[string]bool{
	"cl/compile.go":               true,
	"cl/coro_emission_session.go": true,
}

var allowedCoroBodyCapabilityFiles = map[string]bool{
	"cl/coro_abi.go":               true,
	"cl/coro_await.go":             true,
	"cl/coro_channel.go":           true,
	"cl/coro_critical_lowering.go": true,
	"cl/coro_defer.go":             true,
	"cl/coro_dynamic_await.go":     true,
	"cl/coro_emission_session.go":  true,
	"cl/coro_emitter_adapter.go":   true,
	"cl/coro_implicit_fault.go":    true,
	"cl/coro_lowered_call.go":      true,
	"cl/coro_panic.go":             true,
	"cl/coro_park_emitter.go":      true,
	"cl/coro_patch_init.go":        true,
	"cl/coro_recover.go":           true,
	"cl/coro_slice_to_array.go":    true,
	"cl/coro_spawn.go":             true,
	"cl/coro_unsafe_slice.go":      true,
	"cl/coro_unsafe_string.go":     true,
	"cl/coro_worker.go":            true,
}

var allowedPhysicalEmissionSessionFields = map[string]bool{
	"phase":           true,
	"plan":            true,
	"body":            true,
	"site":            true,
	"sourceBlocks":    true,
	"sourceParamBase": true,
	"explicitStatus":  true,
}

var allowedStagedCoroFeatureNames = map[string]bool{}

var allowedExecutorSourceCatalogFields = map[string]bool{
	"Waits": true, "Timers": true, "Poll": true, "Manual": true,
	"Worker": true, "Channel": true, "Control": true,
}

var allowedRawHelperPlanFiles = map[string]bool{
	"cl/emission_runtime_helpers.go": true,
}

var allowedRawHelperPhysicalTypeFiles = map[string]bool{}

var allowedSiteEmissionEntryFiles = map[string]bool{
	"cl/compile.go": true,
}

var allowedRelocatedEntryFiles = map[string]bool{
	"cl/coro_abi.go":   true,
	"cl/coro_defer.go": true,
}

var allowedHelperObservationFiles = map[string]bool{
	"cl/coro_lowered_call.go": true,
	"cl/coro_defer.go":        true,
}

var allowedProgramIRBuilderAuthorityFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
}

var allowedRawIntrinsicPlanFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
	"cl/emission_universe.go":   true,
}

var allowedRawNoInitPlanFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
	"cl/import.go":              true,
}

var allowedRawIntrinsicOpcodeFiles = map[string]bool{
	"cl/coro_call_site_plan.go":            true,
	"cl/coro_callable_shadow.go":           true,
	"cl/coro_worker_syscall_capability.go": true,
	"cl/emission_universe.go":              true,
}

var allowedRawIntrinsicShapeFiles = map[string]bool{
	"cl/coro_callable_shadow.go":           true,
	"cl/coro_worker_syscall_capability.go": true,
	"cl/emission_universe.go":              true,
}

var allowedRawWorkerCertificateStoreFiles = map[string]bool{
	"cl/coro_call_site_plan.go":            true,
	"cl/coro_worker_syscall_capability.go": true,
	"cl/emission_universe.go":              true,
}

var allowedRawWorkerGraphStoreFiles = map[string]bool{
	"cl/coro_call_site_plan.go":            true,
	"cl/coro_worker_syscall_capability.go": true,
	"cl/emission_universe.go":              true,
}

var allowedPatchRedirectLookupFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
	"cl/coro_patch_init.go":     true,
	"cl/emission_universe.go":   true,
}

var allowedRawPatchRedirectStoreFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
	"cl/emission_universe.go":   true,
}

var allowedCallSiteFreezeFiles = map[string]bool{
	"cl/emission_universe.go": true,
}

var allowedIntrinsicObservationFiles = map[string]bool{
	"cl/instr.go": true,
}

var allowedElisionObservationFiles = map[string]bool{
	"cl/coro_patch_init.go": true,
	"cl/coro_site_plan.go":  true,
	"cl/instr.go":           true,
}

var allowedElisionSelectionFiles = map[string]bool{
	"cl/coro_site_plan.go": true,
	"cl/instr.go":          true,
}

var allowedIntrinsicSelectionFiles = map[string]bool{
	"cl/coro_site_plan.go": true,
	"cl/instr.go":          true,
}

var allowedPhysicalPlanBuildFiles = map[string]bool{
	"cl/coro_abi.go": true,
}

var allowedPhysicalPlanFreezeFiles = map[string]bool{
	"cl/coro_entry.go": true,
}

var allowedPhysicalPlanCommitFiles = map[string]bool{
	"cl/coro_entry.go": true,
}

var allowedPhysicalPlanLookupFiles = map[string]bool{
	"cl/coro_abi.go":   true,
	"cl/coro_entry.go": true,
}

var allowedPhysicalRecipeSelectionFiles = map[string]bool{
	"cl/compile.go": true,
	"cl/instr.go":   true,
}

var allowedPhysicalRecipeObservationFiles = map[string]bool{
	"cl/compile.go": true,
	"cl/instr.go":   true,
}

var allowedPhysicalGuardObservationFiles = map[string]bool{
	"cl/compile.go":             true,
	"cl/coro_implicit_fault.go": true,
	"cl/coro_slice_to_array.go": true,
	"cl/instr.go":               true,
}

var allowedPhysicalCodegenRebuildFiles = map[string]bool{}

var allowedPhysicalProofBuilderCallFiles = map[string]bool{
	"cl/coro_abi.go":      true,
	"cl/coro_defer.go":    true,
	"cl/coro_pure_ssa.go": true,
}

var allowedLegacyPhysicalSelectorFiles = map[string]bool{}

var allowedParkProtocolTemplateFiles = map[string]bool{
	"cl/coro_park_emitter.go": true,
}

var migratedParkProtocolFiles = map[string]bool{
	"cl/coro_channel.go":     true,
	"cl/coro_poll_wait.go":   true,
	"cl/coro_timer_sleep.go": true,
	"cl/coro_worker.go":      true,
}

var migratedParkProtocolFunctions = map[string]bool{
	"compileCoroChanRecv":            true,
	"compileCoroChanSelect":          true,
	"compileCoroChanSend":            true,
	"compileCoroControlledTimerWait": true,
	"compileCoroPollWait":            true,
	"compileCoroTimerSleep":          true,
	"compileCoroWorkerWordCall":      true,
}

var allowedLegacyParkProtocolStepFiles = map[string]bool{}

var allowedRawLocalBodyScanFiles = map[string]bool{
	"internal/coro/ssa_plan.go": true,
}

var allowedLocalBodyFactAuthorityFiles = map[string]bool{
	"cl/coro_call_site_plan.go": true,
	"internal/build/build.go":   true,
}

var allowedSemanticRecipePlanFiles = map[string]bool{
	"cl/coro_physical_plan.go": true,
	"cl/coro_program_ir.go":    true,
	"cl/coro_semantic_plan.go": true,
}

var allowedSemanticRecipeObservationFiles = map[string]bool{
	"cl/compile.go":        true,
	"cl/coro_site_plan.go": true,
}

var allowedPhysicalControlPlanFiles = map[string]bool{
	"cl/coro_physical_plan.go": true,
}

var allowedPhysicalControlSelectionFiles = map[string]bool{
	"cl/coro_emitter_adapter.go": true,
	"cl/coro_site_plan.go":       true,
	"cl/coro_spawn.go":           true,
}

var allowedPhysicalControlObservationFiles = map[string]bool{
	"cl/coro_emitter_adapter.go": true,
	"cl/coro_site_plan.go":       true,
	"cl/coro_spawn.go":           true,
}

var allowedPhysicalOperationPlanFiles = map[string]bool{
	"cl/coro_physical_plan.go": true,
}

var allowedPhysicalOperationSelectionFiles = map[string]bool{
	"cl/compile.go":        true,
	"cl/coro_site_plan.go": true,
	"cl/instr.go":          true,
}

var allowedPhysicalOperationObservationFiles = map[string]bool{
	"cl/compile.go":              true,
	"cl/coro_emitter_adapter.go": true,
	"cl/coro_site_plan.go":       true,
	"cl/instr.go":                true,
}

var allowedPhysicalOutcomePlanFiles = map[string]bool{
	"cl/coro_physical_plan.go": true,
}

var allowedPhysicalOutcomeSelectionFiles = map[string]bool{
	"cl/compile.go":        true,
	"cl/coro_site_plan.go": true,
	"cl/instr.go":          true,
}

var allowedPhysicalOutcomeObservationFiles = map[string]bool{
	"cl/compile.go":        true,
	"cl/coro_site_plan.go": true,
	"cl/instr.go":          true,
}

var allowedCoroParkOperationFields = map[string]bool{
	"shouldSuspend": true,
	"park":          true,
	"resume":        true,
	"normal":        true,
	"faults":        true,
	"abort":         true,
	"shutdown":      true,
}

var allowedCoroParkFaultRouteFields = map[string]bool{
	"status": true,
	"kind":   true,
}

type coroArchitectureDebtInventory struct {
	coroArchitectureDebtBudget
	currentCoroFiles               map[string]bool
	featureNames                   map[string]bool
	sourceFields                   map[string]bool
	rawHelperFiles                 map[string]bool
	rawHelperPhysicalTypeFiles     map[string]bool
	siteEntryFiles                 map[string]bool
	relocatedFiles                 map[string]bool
	observationFiles               map[string]bool
	programIRBuilderAuthorityFiles map[string]bool
	rawNoInitFiles                 map[string]bool
	rawIntrinsicFiles              map[string]bool
	rawIntrinsicOpcodeFiles        map[string]bool
	rawIntrinsicShapeFiles         map[string]bool
	rawWorkerCertificateStoreFiles map[string]bool
	rawWorkerGraphStoreFiles       map[string]bool
	rawPatchRedirectStoreFiles     map[string]bool
	patchRedirectLookupFiles       map[string]bool
	callSiteFreezeFiles            map[string]bool
	intrinsicObservationFiles      map[string]bool
	elisionObservationFiles        map[string]bool
	elisionSelectionFiles          map[string]bool
	intrinsicSelectionFiles        map[string]bool
	physicalPlanBuildFiles         map[string]bool
	physicalPlanFreezeFiles        map[string]bool
	physicalPlanCommitFiles        map[string]bool
	physicalPlanLookupFiles        map[string]bool
	physicalRecipeSelectionFiles   map[string]bool
	physicalRecipeObservationFiles map[string]bool
	physicalGuardObservationFiles  map[string]bool
	physicalCodegenRebuildFiles    map[string]bool
	physicalProofBuilderCallFiles  map[string]bool
	legacyPhysicalSelectorFiles    map[string]bool
	emissionSessionAccessFiles     map[string]bool
	bodyCapabilityAccessFiles      map[string]bool
	emissionSessionFields          map[string]bool
	parkProtocolTemplateFiles      map[string]bool
	parkProtocolEmissionFiles      map[string]bool
	parkProtocolEmissionFunctions  map[string]bool
	legacyParkProtocolStepFiles    map[string]bool
	rawLocalBodyScanFiles          map[string]bool
	localBodyFactAuthorityFiles    map[string]bool
	semanticRecipePlanFiles        map[string]bool
	semanticRecipeObservationFiles map[string]bool
	controlPlanFiles               map[string]bool
	controlSelectFiles             map[string]bool
	controlObserveFiles            map[string]bool
	operationPlanFiles             map[string]bool
	operationSelectFiles           map[string]bool
	operationObserveFiles          map[string]bool
	outcomePlanFiles               map[string]bool
	outcomeSelectFiles             map[string]bool
	outcomeObserveFiles            map[string]bool
	parkProtocolFields             map[string]bool
	parkFaultRouteFields           map[string]bool
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
	check("raw helper planner boundary", inventory.rawHelperPlan, budget.rawHelperPlan)
	check("raw helper physical-type dependency", inventory.rawHelperPhysicalType, budget.rawHelperPhysicalType)
	check("legacy downstream helper scan", inventory.legacyHelperScan, budget.legacyHelperScan)
	check("physical SitePlan emission entry", inventory.siteEmissionEntry, budget.siteEmissionEntry)
	check("relocated SitePlan emission entry", inventory.relocatedEntry, budget.relocatedEntry)
	check("runtime helper SitePlan observation", inventory.helperObservation, budget.helperObservation)
	check("ProgramIR builder authority", inventory.programIRBuilderAuthority, budget.programIRBuilderAuthority)
	check("raw no-init planner boundary", inventory.rawNoInitPlan, budget.rawNoInitPlan)
	check("raw intrinsic planner boundary", inventory.rawIntrinsicPlan, budget.rawIntrinsicPlan)
	check("raw intrinsic opcode boundary", inventory.rawIntrinsicOpcode, budget.rawIntrinsicOpcode)
	check("raw intrinsic shape boundary", inventory.rawIntrinsicShape, budget.rawIntrinsicShape)
	check("raw worker certificate storage boundary", inventory.rawWorkerCertificateStore, budget.rawWorkerCertificateStore)
	check("raw worker graph storage boundary", inventory.rawWorkerGraphStore, budget.rawWorkerGraphStore)
	check("raw patch redirect storage boundary", inventory.rawPatchRedirectStore, budget.rawPatchRedirectStore)
	check("patch redirect lookup boundary", inventory.patchRedirectLookup, budget.patchRedirectLookup)
	check("legacy intrinsic/elision inputs", inventory.legacyIntrinsicInput, budget.legacyIntrinsicInput)
	check("call SitePlan freeze entry", inventory.callSiteFreeze, budget.callSiteFreeze)
	check("intrinsic emission observation", inventory.intrinsicObservation, budget.intrinsicObservation)
	check("call elision observation", inventory.elisionObservation, budget.elisionObservation)
	check("call elision selection", inventory.elisionSelection, budget.elisionSelection)
	check("intrinsic recipe selection", inventory.intrinsicSelection, budget.intrinsicSelection)
	check("physical plan build", inventory.physicalPlanBuild, budget.physicalPlanBuild)
	check("physical plan freeze", inventory.physicalPlanFreeze, budget.physicalPlanFreeze)
	check("physical plan commit", inventory.physicalPlanCommit, budget.physicalPlanCommit)
	check("physical plan lookup", inventory.physicalPlanLookup, budget.physicalPlanLookup)
	check("physical recipe selection", inventory.physicalRecipeSelection, budget.physicalRecipeSelection)
	check("physical recipe observation", inventory.physicalRecipeObservation, budget.physicalRecipeObservation)
	check("physical guard observation", inventory.physicalGuardObservation, budget.physicalGuardObservation)
	check("physical codegen proof rebuild", inventory.physicalCodegenRebuild, budget.physicalCodegenRebuild)
	check("physical proof builder call", inventory.physicalProofBuilderCall, budget.physicalProofBuilderCall)
	check("legacy physical fault selector", inventory.legacyPhysicalSelector, budget.legacyPhysicalSelector)
	check("legacy split physical-emission state", inventory.legacySplitEmissionState, budget.legacySplitEmissionState)
	check("physical emission session field access", inventory.emissionSessionAccess, budget.emissionSessionAccess)
	check("physical body capability access", inventory.bodyCapabilityAccess, budget.bodyCapabilityAccess)
	check("physical emission session begin", inventory.emissionSessionBegin, budget.emissionSessionBegin)
	check("physical body bind", inventory.emissionBodyBind, budget.emissionBodyBind)
	check("physical body completion", inventory.emissionBodyComplete, budget.emissionBodyComplete)
	check("legacy context physical-emission fields", inventory.legacyContextState, budget.legacyContextState)
	check("context physical-emission session field", inventory.contextSessionField, budget.contextSessionField)
	check("Park protocol template", inventory.parkProtocolTemplate, budget.parkProtocolTemplate)
	check("Park protocol emission", inventory.parkProtocolEmission, budget.parkProtocolEmission)
	check("legacy Park protocol steps/state", inventory.legacyParkProtocolStep, budget.legacyParkProtocolStep)
	check("raw local-body scanner boundary", inventory.rawLocalBodyScan, budget.rawLocalBodyScan)
	check("ProgramIR local-body fact authority", inventory.localBodyFactAuthority, budget.localBodyFactAuthority)
	check("semantic recipe planner boundary", inventory.semanticRecipePlan, budget.semanticRecipePlan)
	check("semantic recipe observation", inventory.semanticRecipeObservation, budget.semanticRecipeObservation)
	check("physical control recipe planner", inventory.controlPlan, budget.controlPlan)
	check("physical control recipe selection", inventory.controlSelect, budget.controlSelect)
	check("physical control recipe observation", inventory.controlObserve, budget.controlObserve)
	check("physical operation recipe planner", inventory.operationPlan, budget.operationPlan)
	check("physical operation recipe selection", inventory.operationSelect, budget.operationSelect)
	check("physical operation recipe observation", inventory.operationObserve, budget.operationObserve)
	check("physical outcome recipe planner", inventory.outcomePlan, budget.outcomePlan)
	check("physical outcome recipe selection", inventory.outcomeSelect, budget.outcomeSelect)
	check("physical outcome recipe observation", inventory.outcomeObserve, budget.outcomeObserve)

	checkExactCoroArchitectureSet(t, "currentCoro production files", inventory.currentCoroFiles, allowedCurrentCoroFiles)
	checkExactCoroArchitectureSet(t, "staged coroutine feature names", inventory.featureNames, allowedStagedCoroFeatureNames)
	checkExactCoroArchitectureSet(t, "ExecutorSourceCatalog fields", inventory.sourceFields, allowedExecutorSourceCatalogFields)
	checkExactCoroArchitectureSet(t, "raw helper planner production files", inventory.rawHelperFiles, allowedRawHelperPlanFiles)
	checkExactCoroArchitectureSet(t, "raw helper physical-type dependency files", inventory.rawHelperPhysicalTypeFiles, allowedRawHelperPhysicalTypeFiles)
	checkExactCoroArchitectureSet(t, "physical SitePlan emission entry files", inventory.siteEntryFiles, allowedSiteEmissionEntryFiles)
	checkExactCoroArchitectureSet(t, "relocated SitePlan emission entry files", inventory.relocatedFiles, allowedRelocatedEntryFiles)
	checkExactCoroArchitectureSet(t, "runtime helper SitePlan observation files", inventory.observationFiles, allowedHelperObservationFiles)
	checkExactCoroArchitectureSet(t, "ProgramIR builder authority files", inventory.programIRBuilderAuthorityFiles, allowedProgramIRBuilderAuthorityFiles)
	checkExactCoroArchitectureSet(t, "raw no-init planner production files", inventory.rawNoInitFiles, allowedRawNoInitPlanFiles)
	checkExactCoroArchitectureSet(t, "raw intrinsic planner production files", inventory.rawIntrinsicFiles, allowedRawIntrinsicPlanFiles)
	checkExactCoroArchitectureSet(t, "raw intrinsic opcode production files", inventory.rawIntrinsicOpcodeFiles, allowedRawIntrinsicOpcodeFiles)
	checkExactCoroArchitectureSet(t, "raw intrinsic shape production files", inventory.rawIntrinsicShapeFiles, allowedRawIntrinsicShapeFiles)
	checkExactCoroArchitectureSet(t, "raw worker certificate storage files", inventory.rawWorkerCertificateStoreFiles, allowedRawWorkerCertificateStoreFiles)
	checkExactCoroArchitectureSet(t, "raw worker graph storage files", inventory.rawWorkerGraphStoreFiles, allowedRawWorkerGraphStoreFiles)
	checkExactCoroArchitectureSet(t, "raw patch redirect storage files", inventory.rawPatchRedirectStoreFiles, allowedRawPatchRedirectStoreFiles)
	checkExactCoroArchitectureSet(t, "patch redirect lookup production files", inventory.patchRedirectLookupFiles, allowedPatchRedirectLookupFiles)
	checkExactCoroArchitectureSet(t, "call SitePlan freeze files", inventory.callSiteFreezeFiles, allowedCallSiteFreezeFiles)
	checkExactCoroArchitectureSet(t, "intrinsic emission observation files", inventory.intrinsicObservationFiles, allowedIntrinsicObservationFiles)
	checkExactCoroArchitectureSet(t, "call elision observation files", inventory.elisionObservationFiles, allowedElisionObservationFiles)
	checkExactCoroArchitectureSet(t, "call elision selection files", inventory.elisionSelectionFiles, allowedElisionSelectionFiles)
	checkExactCoroArchitectureSet(t, "intrinsic recipe selection files", inventory.intrinsicSelectionFiles, allowedIntrinsicSelectionFiles)
	checkExactCoroArchitectureSet(t, "physical plan build files", inventory.physicalPlanBuildFiles, allowedPhysicalPlanBuildFiles)
	checkExactCoroArchitectureSet(t, "physical plan freeze files", inventory.physicalPlanFreezeFiles, allowedPhysicalPlanFreezeFiles)
	checkExactCoroArchitectureSet(t, "physical plan commit files", inventory.physicalPlanCommitFiles, allowedPhysicalPlanCommitFiles)
	checkExactCoroArchitectureSet(t, "physical plan lookup files", inventory.physicalPlanLookupFiles, allowedPhysicalPlanLookupFiles)
	checkExactCoroArchitectureSet(t, "physical recipe selection files", inventory.physicalRecipeSelectionFiles, allowedPhysicalRecipeSelectionFiles)
	checkExactCoroArchitectureSet(t, "physical recipe observation files", inventory.physicalRecipeObservationFiles, allowedPhysicalRecipeObservationFiles)
	checkExactCoroArchitectureSet(t, "physical guard observation files", inventory.physicalGuardObservationFiles, allowedPhysicalGuardObservationFiles)
	checkExactCoroArchitectureSet(t, "physical codegen proof rebuild files", inventory.physicalCodegenRebuildFiles, allowedPhysicalCodegenRebuildFiles)
	checkExactCoroArchitectureSet(t, "physical proof builder call files", inventory.physicalProofBuilderCallFiles, allowedPhysicalProofBuilderCallFiles)
	checkExactCoroArchitectureSet(t, "legacy physical fault selector files", inventory.legacyPhysicalSelectorFiles, allowedLegacyPhysicalSelectorFiles)
	checkExactCoroArchitectureSet(t, "physical emission session field access files", inventory.emissionSessionAccessFiles, allowedEmissionSessionAccessFiles)
	checkExactCoroArchitectureSet(t, "physical body capability access files", inventory.bodyCapabilityAccessFiles, allowedCoroBodyCapabilityFiles)
	checkExactCoroArchitectureSet(t, "physical emission session fields", inventory.emissionSessionFields, allowedPhysicalEmissionSessionFields)
	checkExactCoroArchitectureSet(t, "Park protocol template files", inventory.parkProtocolTemplateFiles, allowedParkProtocolTemplateFiles)
	checkExactCoroArchitectureSet(t, "Park protocol emission files", inventory.parkProtocolEmissionFiles, migratedParkProtocolFiles)
	checkExactCoroArchitectureSet(t, "Park protocol emission functions", inventory.parkProtocolEmissionFunctions, migratedParkProtocolFunctions)
	checkExactCoroArchitectureSet(t, "legacy Park protocol step files", inventory.legacyParkProtocolStepFiles, allowedLegacyParkProtocolStepFiles)
	checkExactCoroArchitectureSet(t, "raw local-body scanner files", inventory.rawLocalBodyScanFiles, allowedRawLocalBodyScanFiles)
	checkExactCoroArchitectureSet(t, "ProgramIR local-body fact authority files", inventory.localBodyFactAuthorityFiles, allowedLocalBodyFactAuthorityFiles)
	checkExactCoroArchitectureSet(t, "semantic recipe planner files", inventory.semanticRecipePlanFiles, allowedSemanticRecipePlanFiles)
	checkExactCoroArchitectureSet(t, "semantic recipe observation files", inventory.semanticRecipeObservationFiles, allowedSemanticRecipeObservationFiles)
	checkExactCoroArchitectureSet(t, "physical control recipe planner files", inventory.controlPlanFiles, allowedPhysicalControlPlanFiles)
	checkExactCoroArchitectureSet(t, "physical control recipe selection files", inventory.controlSelectFiles, allowedPhysicalControlSelectionFiles)
	checkExactCoroArchitectureSet(t, "physical control recipe observation files", inventory.controlObserveFiles, allowedPhysicalControlObservationFiles)
	checkExactCoroArchitectureSet(t, "physical operation recipe planner files", inventory.operationPlanFiles, allowedPhysicalOperationPlanFiles)
	checkExactCoroArchitectureSet(t, "physical operation recipe selection files", inventory.operationSelectFiles, allowedPhysicalOperationSelectionFiles)
	checkExactCoroArchitectureSet(t, "physical operation recipe observation files", inventory.operationObserveFiles, allowedPhysicalOperationObservationFiles)
	checkExactCoroArchitectureSet(t, "physical outcome recipe planner files", inventory.outcomePlanFiles, allowedPhysicalOutcomePlanFiles)
	checkExactCoroArchitectureSet(t, "physical outcome recipe selection files", inventory.outcomeSelectFiles, allowedPhysicalOutcomeSelectionFiles)
	checkExactCoroArchitectureSet(t, "physical outcome recipe observation files", inventory.outcomeObserveFiles, allowedPhysicalOutcomeObservationFiles)
	checkExactCoroArchitectureSet(t, "Park protocol fields", inventory.parkProtocolFields, allowedCoroParkOperationFields)
	checkExactCoroArchitectureSet(t, "Park fault-route fields", inventory.parkFaultRouteFields, allowedCoroParkFaultRouteFields)
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
		currentCoroFiles:               make(map[string]bool),
		featureNames:                   make(map[string]bool),
		sourceFields:                   make(map[string]bool),
		rawHelperFiles:                 make(map[string]bool),
		rawHelperPhysicalTypeFiles:     make(map[string]bool),
		siteEntryFiles:                 make(map[string]bool),
		relocatedFiles:                 make(map[string]bool),
		observationFiles:               make(map[string]bool),
		programIRBuilderAuthorityFiles: make(map[string]bool),
		rawNoInitFiles:                 make(map[string]bool),
		rawIntrinsicFiles:              make(map[string]bool),
		rawIntrinsicOpcodeFiles:        make(map[string]bool),
		rawIntrinsicShapeFiles:         make(map[string]bool),
		rawWorkerCertificateStoreFiles: make(map[string]bool),
		rawWorkerGraphStoreFiles:       make(map[string]bool),
		rawPatchRedirectStoreFiles:     make(map[string]bool),
		patchRedirectLookupFiles:       make(map[string]bool),
		callSiteFreezeFiles:            make(map[string]bool),
		intrinsicObservationFiles:      make(map[string]bool),
		elisionObservationFiles:        make(map[string]bool),
		elisionSelectionFiles:          make(map[string]bool),
		intrinsicSelectionFiles:        make(map[string]bool),
		physicalPlanBuildFiles:         make(map[string]bool),
		physicalPlanFreezeFiles:        make(map[string]bool),
		physicalPlanCommitFiles:        make(map[string]bool),
		physicalPlanLookupFiles:        make(map[string]bool),
		physicalRecipeSelectionFiles:   make(map[string]bool),
		physicalRecipeObservationFiles: make(map[string]bool),
		physicalGuardObservationFiles:  make(map[string]bool),
		physicalCodegenRebuildFiles:    make(map[string]bool),
		physicalProofBuilderCallFiles:  make(map[string]bool),
		legacyPhysicalSelectorFiles:    make(map[string]bool),
		emissionSessionAccessFiles:     make(map[string]bool),
		bodyCapabilityAccessFiles:      make(map[string]bool),
		emissionSessionFields:          make(map[string]bool),
		parkProtocolTemplateFiles:      make(map[string]bool),
		parkProtocolEmissionFiles:      make(map[string]bool),
		parkProtocolEmissionFunctions:  make(map[string]bool),
		legacyParkProtocolStepFiles:    make(map[string]bool),
		rawLocalBodyScanFiles:          make(map[string]bool),
		localBodyFactAuthorityFiles:    make(map[string]bool),
		semanticRecipePlanFiles:        make(map[string]bool),
		semanticRecipeObservationFiles: make(map[string]bool),
		controlPlanFiles:               make(map[string]bool),
		controlSelectFiles:             make(map[string]bool),
		controlObserveFiles:            make(map[string]bool),
		operationPlanFiles:             make(map[string]bool),
		operationSelectFiles:           make(map[string]bool),
		operationObserveFiles:          make(map[string]bool),
		outcomePlanFiles:               make(map[string]bool),
		outcomeSelectFiles:             make(map[string]bool),
		outcomeObserveFiles:            make(map[string]bool),
		parkProtocolFields:             make(map[string]bool),
		parkFaultRouteFields:           make(map[string]bool),
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
			type functionSpan struct {
				name       string
				start, end token.Pos
			}
			var functionSpans []functionSpan
			var physicalBodyStart, physicalBodyEnd token.Pos
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				functionSpans = append(functionSpans, functionSpan{
					name: function.Name.Name, start: function.Body.Pos(), end: function.Body.End(),
				})
				if function.Name.Name == "compileCoroPhysicalBody" {
					physicalBodyStart, physicalBodyEnd = function.Body.Pos(), function.Body.End()
				}
			}
			enclosingFunction := func(node ast.Node) string {
				for _, span := range functionSpans {
					if node.Pos() >= span.start && node.End() <= span.end {
						return span.name
					}
				}
				return "<outside-function>"
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					callName := ""
					switch function := node.Fun.(type) {
					case *ast.SelectorExpr:
						callName = function.Sel.Name
					case *ast.Ident:
						callName = function.Name
					}
					if callName == "emitCoroParkOperation" {
						inventory.parkProtocolEmission++
						inventory.parkProtocolEmissionFiles[rel] = true
						inventory.parkProtocolEmissionFunctions[enclosingFunction(node)] = true
					}
					if callName == "SuspendCurrentBlockIfWithResumeDispatch" {
						inventory.parkProtocolTemplate++
						inventory.parkProtocolTemplateFiles[rel] = true
					}
					if migratedParkProtocolFiles[rel] {
						switch callName {
						case "publishState", "cancellationRunDecisionTargets":
							inventory.legacyParkProtocolStep++
							inventory.legacyParkProtocolStepFiles[rel] = true
						case "activate":
							if migratedParkProtocolFunctions[enclosingFunction(node)] {
								inventory.legacyParkProtocolStep++
								inventory.legacyParkProtocolStepFiles[rel] = true
							}
						}
					}
					if physicalBodyStart.IsValid() && node.Pos() >= physicalBodyStart && node.End() <= physicalBodyEnd {
						switch callName {
						case "newCoroPhysicalPureSSAAudit", "newCoroPhysicalPureSSAAuditForOwner",
							"proveCoroCriticalRegions", "prepareCoroStaticCleanupPlan":
							inventory.physicalCodegenRebuild++
							inventory.physicalCodegenRebuildFiles[rel] = true
						}
					}
					switch callName {
					case "newCoroPhysicalPureSSAAudit", "newCoroPhysicalPureSSAAuditForOwner",
						"proveCoroCriticalRegions", "prepareCoroStaticCleanupPlan":
						inventory.physicalProofBuilderCall++
						inventory.physicalProofBuilderCallFiles[rel] = true
					}
					if callName == "prepareCoroPhysicalFunctionPlan" {
						inventory.physicalPlanBuild++
						inventory.physicalPlanBuildFiles[rel] = true
					}
					selector, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						break
					}
					switch selector.Sel.Name {
					case "beginCoroSiteEmission":
						inventory.siteEmissionEntry++
						inventory.siteEntryFiles[rel] = true
					case "beginCoroRelocatedSiteEmission":
						inventory.relocatedEntry++
						inventory.relocatedFiles[rel] = true
					case "observeCoroSiteRuntimeHelper":
						inventory.helperObservation++
						inventory.observationFiles[rel] = true
					case "freezeCallSites":
						inventory.callSiteFreeze++
						inventory.callSiteFreezeFiles[rel] = true
					case "observeCoroIntrinsicCallEmission":
						inventory.intrinsicObservation++
						inventory.intrinsicObservationFiles[rel] = true
					case "observeCoroCallElision":
						inventory.elisionObservation++
						inventory.elisionObservationFiles[rel] = true
					case "freezePhysicalFunctionPlan":
						inventory.physicalPlanFreeze++
						inventory.physicalPlanFreezeFiles[rel] = true
					case "commitPhysicalFunctionPlans":
						inventory.physicalPlanCommit++
						inventory.physicalPlanCommitFiles[rel] = true
					case "physicalFunctionPlan":
						inventory.physicalPlanLookup++
						inventory.physicalPlanLookupFiles[rel] = true
					case "plannedCoroPhysicalInstruction":
						inventory.physicalRecipeSelection++
						inventory.physicalRecipeSelectionFiles[rel] = true
					case "observeCoroPhysicalInstruction":
						inventory.physicalRecipeObservation++
						inventory.physicalRecipeObservationFiles[rel] = true
					case "observeCoroPhysicalNilGuard", "observeCoroPhysicalBoundsGuard":
						inventory.physicalGuardObservation++
						inventory.physicalGuardObservationFiles[rel] = true
					case "beginCoroPhysicalEmission":
						inventory.emissionSessionBegin++
					case "bindCoroPhysicalBody":
						inventory.emissionBodyBind++
					case "completeCoroPhysicalBody":
						inventory.emissionBodyComplete++
					case "type_":
						if rel == "cl/emission_runtime_helpers.go" {
							inventory.rawHelperPhysicalType++
							inventory.rawHelperPhysicalTypeFiles[rel] = true
						}
					}
				case *ast.Ident:
					name := node.Name
					if migratedParkProtocolFiles[rel] {
						switch name {
						case "nextState", "instructions", "coroSuspendPark", "coroLifecycleSuspended":
							inventory.legacyParkProtocolStep++
							inventory.legacyParkProtocolStepFiles[rel] = true
						}
					}
					switch name {
					case "scanSSAFunctionBody":
						inventory.rawLocalBodyScan++
						inventory.rawLocalBodyScanFiles[rel] = true
					case "CoroLocalBodyFacts":
						inventory.localBodyFactAuthority++
						inventory.localBodyFactAuthorityFiles[rel] = true
					case "planCoroSemanticInstruction":
						inventory.semanticRecipePlan++
						inventory.semanticRecipePlanFiles[rel] = true
					case "observeCoroSemanticInstruction":
						inventory.semanticRecipeObservation++
						inventory.semanticRecipeObservationFiles[rel] = true
					case "planCoroPhysicalControlInstruction":
						inventory.controlPlan++
						inventory.controlPlanFiles[rel] = true
					case "plannedCoroPhysicalControl":
						inventory.controlSelect++
						inventory.controlSelectFiles[rel] = true
					case "observeCoroPhysicalControl":
						inventory.controlObserve++
						inventory.controlObserveFiles[rel] = true
					case "planCoroPhysicalOperationInstruction":
						inventory.operationPlan++
						inventory.operationPlanFiles[rel] = true
					case "plannedCoroPhysicalOperation":
						inventory.operationSelect++
						inventory.operationSelectFiles[rel] = true
					case "observeCoroPhysicalOperation":
						inventory.operationObserve++
						inventory.operationObserveFiles[rel] = true
					case "planCoroPhysicalOutcomeInstruction":
						inventory.outcomePlan++
						inventory.outcomePlanFiles[rel] = true
					case "plannedCoroPhysicalOutcome":
						inventory.outcomeSelect++
						inventory.outcomeSelectFiles[rel] = true
					case "observeCoroPhysicalOutcome":
						inventory.outcomeObserve++
						inventory.outcomeObserveFiles[rel] = true
					case "currentCoro":
						inventory.currentCoro++
						inventory.currentCoroFiles[rel] = true
					case "currentCoroSite", "coroPhysicalPlan", "coroPhysicalEmission",
						"coroExplicitStatus", "coroSourceBlocks":
						inventory.legacySplitEmissionState++
					case "coroEmission":
						if strings.HasPrefix(rel, "cl/") {
							inventory.emissionSessionAccess++
							inventory.emissionSessionAccessFiles[rel] = true
						}
					case "coroBody":
						inventory.bodyCapabilityAccess++
						inventory.bodyCapabilityAccessFiles[rel] = true
					case "CoroPlan":
						inventory.planAuthority++
					case "EmissionUniverse":
						if allowedProgramIRBuilderAuthorityFiles[rel] {
							inventory.programIRBuilderAuthority++
							inventory.programIRBuilderAuthorityFiles[rel] = true
						} else {
							inventory.planAuthority++
						}
					case "WaitToken":
						inventory.legacyWait++
					case "classifyCoroRuntimeHelpers", "classifyPlainRuntimeHelpers":
						inventory.rawHelperPlan++
						inventory.rawHelperFiles[rel] = true
					case "loweredRuntimeHelpers", "plainRepresentationRuntimeHelpers", "coroRelocatedHelpers":
						inventory.legacyHelperScan++
					case "classifyCoroIntrinsicCallSite":
						inventory.rawIntrinsicPlan++
						inventory.rawIntrinsicFiles[rel] = true
					case "FrontendElidesNoInitCall":
						inventory.rawNoInitPlan++
						inventory.rawNoInitFiles[rel] = true
					case "coroIntrinsicOpcode":
						inventory.rawIntrinsicOpcode++
						inventory.rawIntrinsicOpcodeFiles[rel] = true
					case "workerSyscalls":
						inventory.rawWorkerCertificateStore++
						inventory.rawWorkerCertificateStoreFiles[rel] = true
					case "workerSyscallOwners", "workerSyscallIncoming":
						inventory.rawWorkerGraphStore++
						inventory.rawWorkerGraphStoreFiles[rel] = true
					case "patchInitRedirects":
						inventory.rawPatchRedirectStore++
						inventory.rawPatchRedirectStoreFiles[rel] = true
					case "CoroPatchInitRedirect":
						inventory.patchRedirectLookup++
						inventory.patchRedirectLookupFiles[rel] = true
					case "plannedCoroCallElision":
						inventory.elisionSelection++
						inventory.elisionSelectionFiles[rel] = true
					case "plannedCoroIntrinsicCall":
						inventory.intrinsicSelection++
						inventory.intrinsicSelectionFiles[rel] = true
					case "intrinsicCallSemantics", "patchInitRedirect", "elidedCallCertificate":
						inventory.legacyIntrinsicInput++
					case "coroFieldAddrRequiresImplicitNilFault", "coroDerefRequiresImplicitNilFault",
						"coroIndexOperationMayFault", "compileCoroIndexAddrGuarded",
						"compileCoroIndexGuarded", "compileCoroSliceGuarded":
						inventory.legacyPhysicalSelector++
						inventory.legacyPhysicalSelectorFiles[rel] = true
					case "timerRegistrationModeV1", "pollOperationModeV1", "coroNativeTargetV1State":
						inventory.nativeFork++
					default:
						if strings.HasPrefix(name, "validateCoro") && strings.HasSuffix(name, "IntrinsicCallSite") {
							inventory.rawIntrinsicShape++
							inventory.rawIntrinsicShapeFiles[rel] = true
						}
						if strings.HasPrefix(name, "EnableCoro") {
							inventory.stagedFeatureGate++
							inventory.featureNames[name] = true
						}
						if strings.HasPrefix(name, "coroProgram") && strings.HasSuffix(name, "V1State") {
							inventory.nativeFork++
						}
					}
				case *ast.TypeSpec:
					structure, ok := node.Type.(*ast.StructType)
					if !ok {
						break
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							switch node.Name.Name {
							case "ExecutorSourceCatalog":
								inventory.sourceFields[name.Name] = true
							case "context":
								if rel != "cl/compile.go" {
									continue
								}
								switch name.Name {
								case "currentCoro", "currentCoroSite", "coroPhysicalPlan", "coroPhysicalEmission",
									"coroExplicitStatus", "coroSourceBlocks", "sourceParamBase":
									inventory.legacyContextState++
								case "coroEmission":
									inventory.contextSessionField++
								}
							case "coroPhysicalEmissionSession":
								inventory.emissionSessionFields[name.Name] = true
							case "coroParkOperation":
								inventory.parkProtocolFields[name.Name] = true
							case "coroParkFaultRoute":
								inventory.parkFaultRouteFields[name.Name] = true
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
