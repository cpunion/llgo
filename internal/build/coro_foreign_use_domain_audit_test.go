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
	"fmt"
	"sort"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroForeignUseDomainRecord is a diagnostic projection over one exact C
// declaration.  It grants no execution capability: raw execution was already
// selected and validated by the immutable occurrence plans before this record
// is built.
type coroForeignUseDomainRecord struct {
	Function       *ssa.Function
	FunctionID     coro.FunctionID
	PhysicalSymbol string
	LegacySync     bool
	Calls          int
	RawHostCalls   int
	Rejections     []string
}

func (record coroForeignUseDomainRecord) rawHostOnly() bool {
	return record.Calls != 0 &&
		record.RawHostCalls == record.Calls &&
		len(record.Rejections) == 0
}

func (record coroForeignUseDomainRecord) diagnostic() string {
	status := "reject"
	if record.rawHostOnly() {
		status = "raw-host-only"
	}
	reasons := strings.Join(record.Rejections, ",")
	if reasons == "" {
		reasons = "-"
	}
	return fmt.Sprintf(
		"%s\t%s\tlegacy-sync=%t\tcalls=%d\traw=%d\treasons=%s",
		status, record.PhysicalSymbol, record.LegacySync,
		record.Calls, record.RawHostCalls, reasons,
	)
}

type coroForeignUseDomainReport struct {
	Closed  bool
	Records []coroForeignUseDomainRecord
}

func (report coroForeignUseDomainReport) legacySyncRecords() []coroForeignUseDomainRecord {
	records := make([]coroForeignUseDomainRecord, 0)
	for _, record := range report.Records {
		if record.LegacySync {
			records = append(records, record)
		}
	}
	return records
}

func addCoroForeignUseDomainRejection(record *coroForeignUseDomainRecord, reason string) {
	if record == nil || reason == "" {
		return
	}
	for _, previous := range record.Rejections {
		if previous == reason {
			return
		}
	}
	record.Rejections = append(record.Rejections, reason)
}

func exactCoroForeignUseDomainOperand(
	in CoroPlanInput,
	value ssa.Value,
	records map[*ssa.Function]*coroForeignUseDomainRecord,
) *coroForeignUseDomainRecord {
	function, ok := value.(*ssa.Function)
	if !ok || function == nil {
		return nil
	}
	canonical, resolved := in.ResolveFunction(function)
	if !resolved || canonical == nil {
		return nil
	}
	return records[canonical]
}

func validCoroRawPlainUseOwner(
	fixed *coro.SSAPlan,
	raw *coroRawABIPlainClosure,
	owner *ssa.Function,
) bool {
	if fixed == nil || raw == nil || owner == nil {
		return false
	}
	_, member := raw.functions[owner]
	function, planned := fixed.FunctionPlan(owner)
	return member && planned &&
		function.External == coro.Defined &&
		function.ManagedDemand == coro.NoDemand &&
		function.RawPlainDemand &&
		function.RawPlainOnly &&
		function.Emission == coro.EmitRawPlain &&
		function.Primary == coro.PrimaryPlain &&
		function.FuncRep == coro.DirectPlain &&
		fixed.HasRawPlainVariant(owner)
}

func validCoroRawHostUseOwner(
	fixed *coro.SSAPlan,
	raw *coroRawABIPlainClosure,
	owner *ssa.Function,
) bool {
	if !validCoroRawPlainUseOwner(fixed, raw, owner) {
		return false
	}
	_, hostStack := raw.hostStack[owner]
	return hostStack
}

func inspectCoroForeignUseDomains(
	in CoroPlanInput,
	fixed *coro.SSAPlan,
	raw *coroRawABIPlainClosure,
	closed bool,
) (coroForeignUseDomainReport, error) {
	if fixed == nil {
		return coroForeignUseDomainReport{}, fmt.Errorf("foreign use-domain audit requires a coroutine SSA plan")
	}
	report := coroForeignUseDomainReport{Closed: closed}
	records := make(map[*ssa.Function]*coroForeignUseDomainRecord)
	for _, function := range fixed.Functions() {
		target := function.Function
		if target == nil {
			return coroForeignUseDomainReport{}, fmt.Errorf("foreign use-domain audit encountered a nil function for %q", function.Plan.ID)
		}
		identity, managedC := fixed.CallableIdentityCertificate(target)
		if !managedC {
			continue
		}
		if err := identity.Validate(); err != nil {
			return coroForeignUseDomainReport{}, fmt.Errorf(
				"foreign use-domain callable identity for %q: %w",
				function.Plan.ID, err,
			)
		}
		_, legacySync := fixed.ForeignSyncCertificate(target)
		record := &coroForeignUseDomainRecord{
			Function:       target,
			FunctionID:     function.Plan.ID,
			PhysicalSymbol: identity.PhysicalSymbol,
			LegacySync:     legacySync,
		}
		if !report.Closed {
			addCoroForeignUseDomainRejection(record, "open-emission-universe")
		}
		if function.Plan.Emission != coro.EmitNone &&
			(function.Plan.External == coro.Defined ||
				function.Plan.Emission != coro.EmitExternal) {
			addCoroForeignUseDomainRejection(record, "invalid-external-plan")
		}
		records[target] = record
	}

	// Keep the audit index pointer-only until every use has been classified.
	// The resulting value projection is diagnostic and never becomes a second
	// plan authority.
	for _, ownerRecord := range fixed.Functions() {
		owner := ownerRecord.Function
		if owner == nil || fixed.IgnoresBody(owner) ||
			ownerRecord.Plan.Emission == coro.EmitNone {
			continue
		}
		rawOwner := validCoroRawPlainUseOwner(fixed, raw, owner)
		rawHostOwner := validCoroRawHostUseOwner(fixed, raw, owner)
		for _, lowered := range fixed.LoweredCalls(owner) {
			record := records[lowered.Target]
			if record == nil {
				continue
			}
			record.Calls++
			if rawHostOwner {
				record.RawHostCalls++
			} else if rawOwner || lowered.RawPlain {
				addCoroForeignUseDomainRejection(record, "non-host-raw-lowered-call")
			} else {
				addCoroForeignUseDomainRejection(record, "managed-lowered-call")
			}
		}
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, isCall := instruction.(ssa.CallInstruction)
				common := (*ssa.CallCommon)(nil)
				var staticOperand ssa.Value
				if isCall {
					common = call.Common()
					if common != nil && common.StaticCallee() != nil {
						staticOperand = common.Value
					}
					callPlan, planned := fixed.CallPlan(call)
					if planned {
						for _, targetID := range callPlan.Targets {
							target, found := fixed.Function(targetID)
							if !found || target == nil {
								return coroForeignUseDomainReport{}, fmt.Errorf(
									"foreign use-domain call in %q has unresolved target %q",
									ownerRecord.Plan.ID, targetID,
								)
							}
							record := records[target]
							if record == nil {
								continue
							}
							record.Calls++
							_, ordinary := call.(*ssa.Call)
							exact := ordinary &&
								!callPlan.Open &&
								!callPlan.MayBeNil &&
								len(callPlan.Targets) == 1 &&
								(callPlan.Kind == coro.CallDirect ||
									callPlan.Kind == coro.CallForeign) &&
								callPlan.Transport == coro.ManagedTransport &&
								(callPlan.Rep == coro.DirectPlain ||
									callPlan.Rep == coro.DirectCoro)
							if !exact {
								addCoroForeignUseDomainRejection(record, "non-exact-call")
								continue
							}
							if rawHostOwner {
								record.RawHostCalls++
							} else if callPlan.RawPlain || rawOwner {
								addCoroForeignUseDomainRejection(record, "non-host-raw-call")
							} else {
								addCoroForeignUseDomainRejection(record, "managed-call")
							}
						}
					}
				}
				skippedStaticOperand := false
				for _, operand := range instruction.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					if !skippedStaticOperand && staticOperand != nil &&
						*operand == staticOperand {
						// A physical static callee is classified solely through
						// its immutable CallPlan above.  This also excludes a
						// source C declaration redirected to an exact local Go
						// export: the declaration is not physically consumed.
						skippedStaticOperand = true
						continue
					}
					record := exactCoroForeignUseDomainOperand(in, *operand, records)
					if record != nil {
						addCoroForeignUseDomainRejection(record, "non-call-reference")
					}
				}
			}
		}
	}

	report.Records = make([]coroForeignUseDomainRecord, 0, len(records))
	for _, record := range records {
		targetPlan, planned := fixed.FunctionPlan(record.Function)
		if !planned {
			return coroForeignUseDomainReport{}, fmt.Errorf(
				"foreign use-domain declaration %q lost its function plan",
				record.FunctionID,
			)
		}
		if record.Calls == 0 {
			addCoroForeignUseDomainRejection(record, "no-live-call")
		}
		if record.RawHostCalls != record.Calls {
			addCoroForeignUseDomainRejection(record, "not-raw-host-only")
		}
		if record.RawHostCalls != 0 &&
			(targetPlan.ManagedDemand != coro.NoDemand ||
				!targetPlan.RawPlainDemand ||
				targetPlan.Emission != coro.EmitExternal) {
			addCoroForeignUseDomainRejection(record, "target-has-managed-demand")
		}
		sort.Strings(record.Rejections)
		report.Records = append(report.Records, *record)
	}
	sort.Slice(report.Records, func(left, right int) bool {
		return report.Records[left].FunctionID < report.Records[right].FunctionID
	})
	return report, nil
}
