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
	"slices"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func analyzeForeignUseDomainFixture(
	t *testing.T,
	fixture *foreignCapabilityBuildFixture,
	closed bool,
	managedRoots ...*ssa.Function,
) (*coro.SSAPlan, coroForeignUseDomainReport) {
	t.Helper()
	host := fixture.pkg.Func("Host")
	syncLeaf := fixture.pkg.Func("Sync")
	input := fixture.input
	input.requiredRoots = coro.Roots{{Function: host, Demand: coro.SyncDemand}}
	// The production runtime-plan closure includes a legacy-certified C leaf in
	// requiredHostPlain before the second raw fixed point.  Model that exact
	// pre-migration shape here; the unannotated may-block leaf deliberately
	// remains invocation-scoped and is not added.
	input.requiredPlain = map[*ssa.Function]struct{}{host: {}, syncLeaf: {}}
	input.requiredHostPlain = map[*ssa.Function]struct{}{host: {}, syncLeaf: {}}
	input.syncDemandReferences = func(*ssa.Function) ([]*ssa.Function, error) {
		return nil, nil
	}
	roots := make(coro.Roots, 0, len(managedRoots))
	for _, function := range managedRoots {
		roots = append(roots, coro.Root{Function: function, Demand: coro.SyncDemand})
	}
	plan, err := fixture.analyze(input, roots)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := input.liveCoroRawABIPlainClosure(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := inspectCoroForeignUseDomains(input, plan, raw, closed)
	if err != nil {
		t.Fatal(err)
	}
	return plan, report
}

func foreignUseDomainRecordBySymbol(
	t *testing.T,
	report coroForeignUseDomainReport,
	symbol string,
) coroForeignUseDomainRecord {
	t.Helper()
	var found *coroForeignUseDomainRecord
	for index := range report.Records {
		record := &report.Records[index]
		if record.PhysicalSymbol != symbol {
			continue
		}
		if found != nil {
			t.Fatalf("foreign use-domain symbol %q is ambiguous", symbol)
		}
		found = record
	}
	if found == nil {
		t.Fatalf("foreign use-domain symbol %q is absent", symbol)
	}
	return *found
}

func TestCoroForeignUseDomainInfersOnlyClosedRawHostCalls(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()

	plan, report := analyzeForeignUseDomainFixture(t, fixture, true)
	syncRecord := foreignUseDomainRecordBySymbol(t, report, "foreign_sync_exact")
	syncPlan, _ := plan.FunctionPlan(syncRecord.Function)
	if !report.Closed || !syncRecord.LegacySync || !syncRecord.rawHostOnly() ||
		syncRecord.Calls != 1 || syncRecord.RawHostCalls != 1 {
		t.Fatalf("closed raw-host sync record = %+v, function=%+v report-closed=%t", syncRecord, syncPlan, report.Closed)
	}
	if _, certified := plan.ForeignSyncCertificate(syncRecord.Function); !certified {
		t.Fatal("fixture sync declaration lost its source certificate before the migration audit")
	}

	mayBlockRecord := foreignUseDomainRecordBySymbol(t, report, "foreign_may_block_exact")
	if !mayBlockRecord.rawHostOnly() || mayBlockRecord.LegacySync {
		t.Fatalf("unannotated raw-host default record = %+v", mayBlockRecord)
	}
}

func TestCoroForeignUseDomainRejectsManagedAndEscapedUses(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()

	_, mixed := analyzeForeignUseDomainFixture(
		t,
		fixture,
		true,
		fixture.pkg.Func("Managed"),
	)
	mayBlock := foreignUseDomainRecordBySymbol(t, mixed, "foreign_may_block_exact")
	if mayBlock.rawHostOnly() ||
		!slices.Contains(mayBlock.Rejections, "managed-call") ||
		!slices.Contains(mayBlock.Rejections, "target-has-managed-demand") {
		t.Fatalf("mixed managed/raw foreign record = %+v", mayBlock)
	}

	_, escaped := analyzeForeignUseDomainFixture(
		t,
		fixture,
		true,
		fixture.pkg.Func("EscapeSync"),
	)
	syncRecord := foreignUseDomainRecordBySymbol(t, escaped, "foreign_escaped_sync_exact")
	if syncRecord.rawHostOnly() ||
		!slices.Contains(syncRecord.Rejections, "non-call-reference") {
		t.Fatalf("escaped foreign function record = %+v", syncRecord)
	}
}

func TestCoroForeignUseDomainRejectsOpenEmissionUniverse(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()

	_, report := analyzeForeignUseDomainFixture(t, fixture, false)
	record := foreignUseDomainRecordBySymbol(t, report, "foreign_sync_exact")
	if report.Closed || record.rawHostOnly() ||
		!slices.Contains(record.Rejections, "open-emission-universe") {
		t.Fatalf("open-universe foreign record = %+v, report-closed=%t", record, report.Closed)
	}
}
