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

package cl

import (
	"fmt"
	"go/ast"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

const coroWorkerResultProjectionWidthV1 = 8

// coroWorkerResultProjection is the exact source-owned assertion that one
// internal Go wrapper forwards selected worker result words. It deliberately
// says nothing about pointer-ness: that fact still comes from the exact C
// callable contract carried by one producer-forward incoming edge.
//
// resultToWorker uses zero-based tuple indices internally. -1 means that the
// wrapper result is not projected by the directive.
type coroWorkerResultProjection struct {
	functionParameter int
	resultToWorker    [coroWorkerResultProjectionWidthV1]int8
	canonical         string
}

type coroWorkerResultProjectionCertificate struct {
	id                string
	functionParameter int
	resultToWorker    [coroWorkerResultProjectionWidthV1]int8
}

func emptyCoroWorkerResultProjection() coroWorkerResultProjection {
	projection := coroWorkerResultProjection{functionParameter: -1}
	for index := range projection.resultToWorker {
		projection.resultToWorker[index] = -1
	}
	return projection
}

func parseCoroWorkerResultProjectionDecl(decl *ast.FuncDecl) (coroWorkerResultProjection, bool, error) {
	projection := emptyCoroWorkerResultProjection()
	if decl == nil || decl.Doc == nil {
		return projection, false, nil
	}

	var directive []string
	var directivePayload string
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		line := comment.Text
		if !strings.HasPrefix(line, "//") {
			continue
		}
		payload := strings.TrimPrefix(line, "//")
		fields := strings.Fields(payload)
		if len(fields) < 2 || fields[0] != "llgo:coro" || fields[1] != "workerresult" {
			continue
		}
		if directive != nil {
			return projection, false, fmt.Errorf("duplicate //llgo:coro workerresult directive")
		}
		directive = fields
		directivePayload = payload
	}
	if directive == nil {
		return projection, false, nil
	}
	if decl.Body == nil {
		return projection, false, fmt.Errorf("//llgo:coro workerresult requires a bodyful Go wrapper")
	}
	if len(directive) != 5 || directive[2] != "v1" {
		return projection, false, fmt.Errorf("//llgo:coro workerresult requires exact syntax: //llgo:coro workerresult v1 fn=<index> map=<wrapper>:<worker>[,...]")
	}
	if !strings.HasPrefix(directive[3], "fn=") || !strings.HasPrefix(directive[4], "map=") {
		return projection, false, fmt.Errorf("//llgo:coro workerresult v1 requires canonical fn then map fields")
	}
	parameterText := strings.TrimPrefix(directive[3], "fn=")
	parameter, err := strconv.Atoi(parameterText)
	if err != nil || parameter < 0 || strconv.Itoa(parameter) != parameterText {
		return projection, false, fmt.Errorf("//llgo:coro workerresult v1 has invalid function parameter %q", parameterText)
	}
	projection.functionParameter = parameter

	mappingText := strings.TrimPrefix(directive[4], "map=")
	if mappingText == "" {
		return projection, false, fmt.Errorf("//llgo:coro workerresult v1 requires a non-empty result map")
	}
	lastWrapper := -1
	canonicalMappings := make([]string, 0, strings.Count(mappingText, ",")+1)
	for _, mapping := range strings.Split(mappingText, ",") {
		wrapperText, workerText, ok := strings.Cut(mapping, ":")
		if !ok || strings.Contains(workerText, ":") {
			return projection, false, fmt.Errorf("//llgo:coro workerresult v1 has invalid result mapping %q", mapping)
		}
		wrapper, wrapperOK := parseCoroWorkerResultWord(wrapperText)
		worker, workerOK := parseCoroWorkerResultWord(workerText)
		if !wrapperOK || !workerOK {
			return projection, false, fmt.Errorf("//llgo:coro workerresult v1 has invalid result mapping %q", mapping)
		}
		if wrapper <= lastWrapper {
			return projection, false, fmt.Errorf("//llgo:coro workerresult v1 result mappings must be unique and ordered by wrapper result")
		}
		lastWrapper = wrapper
		projection.resultToWorker[wrapper] = int8(worker)
		canonicalMappings = append(canonicalMappings, coroWorkerResultWord(wrapper)+":"+coroWorkerResultWord(worker))
	}
	projection.canonical = "llgo:coro workerresult v1 fn=" + strconv.Itoa(parameter) + " map=" + strings.Join(canonicalMappings, ",")
	if directivePayload != projection.canonical {
		return projection, false, fmt.Errorf("//llgo:coro workerresult v1 is not in canonical form %q", projection.canonical)
	}
	return projection, true, nil
}

func parseCoroWorkerResultWord(text string) (int, bool) {
	if len(text) != 2 || text[0] != 'r' || text[1] < '1' || text[1] > '8' {
		return 0, false
	}
	return int(text[1] - '1'), true
}

func coroWorkerResultWord(index int) string {
	return "r" + strconv.Itoa(index+1)
}

func coroWorkerResultProjectionFor(fn *ssa.Function) (coroWorkerResultProjection, bool, error) {
	if fn == nil {
		return coroWorkerResultProjection{}, false, nil
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	return parseCoroWorkerResultProjectionDecl(decl)
}

type coroWorkerResultOrigin struct {
	functionParameter int
	workerResult      int
}

// inferCoroWorkerResultOrigin follows only representation-preserving SSA
// operations. It deliberately stops at arithmetic, memory, interfaces and
// calls: those operations may change either pointer provenance or result
// identity and therefore require a different proof.
func inferCoroWorkerResultOrigin(
	value ssa.Value,
	sinks map[*ssa.Call]int,
	visiting map[ssa.Value]bool,
) (coroWorkerResultOrigin, bool) {
	if value == nil || visiting[value] {
		return coroWorkerResultOrigin{}, false
	}
	visiting[value] = true
	defer delete(visiting, value)

	switch value := value.(type) {
	case *ssa.Extract:
		call, ok := value.Tuple.(*ssa.Call)
		if !ok || value.Index < 0 || value.Index >= coroWorkerResultProjectionWidthV1 {
			return coroWorkerResultOrigin{}, false
		}
		parameter, ok := sinks[call]
		if !ok {
			return coroWorkerResultOrigin{}, false
		}
		return coroWorkerResultOrigin{
			functionParameter: parameter,
			workerResult:      value.Index,
		}, true
	case *ssa.ChangeType:
		return inferCoroWorkerResultOrigin(value.X, sinks, visiting)
	case *ssa.Convert:
		if !coroWorkerUintptrType(value.Type()) || !coroWorkerUintptrType(value.X.Type()) {
			return coroWorkerResultOrigin{}, false
		}
		return inferCoroWorkerResultOrigin(value.X, sinks, visiting)
	case *ssa.Phi:
		var joined coroWorkerResultOrigin
		have := false
		for _, edge := range value.Edges {
			origin, ok := inferCoroWorkerResultOrigin(edge, sinks, visiting)
			if !ok {
				return coroWorkerResultOrigin{}, false
			}
			if !have {
				joined = origin
				have = true
				continue
			}
			// Different physical calls are allowed across control-flow arms, but
			// the carrier parameter and result position must be identical.
			if joined.functionParameter != origin.functionParameter ||
				joined.workerResult != origin.workerResult {
				return coroWorkerResultOrigin{}, false
			}
		}
		return joined, have
	default:
		return coroWorkerResultOrigin{}, false
	}
}

func coroWorkerResultProjectionCanonical(projection coroWorkerResultProjection) string {
	mappings := make([]string, 0, len(projection.resultToWorker))
	for wrapper, worker := range projection.resultToWorker {
		if worker >= 0 {
			mappings = append(mappings, coroWorkerResultWord(wrapper)+":"+coroWorkerResultWord(int(worker)))
		}
	}
	return "ssa-result-flow.v1 fn=" + strconv.Itoa(projection.functionParameter) +
		" map=" + strings.Join(mappings, ",")
}

func inferCoroWorkerResultProjection(
	shadows *CoroCallableShadowAnalysis,
	fn *ssa.Function,
) (coroWorkerResultProjection, bool, error) {
	projection := emptyCoroWorkerResultProjection()
	if shadows == nil || fn == nil || len(fn.Blocks) == 0 || fn.Signature == nil {
		return projection, false, nil
	}

	sinks := make(map[*ssa.Call]int)
	returns := make([]*ssa.Return, 0)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.Return:
				returns = append(returns, instruction)
			case *ssa.Call:
				common := instruction.Common()
				if common == nil || common.IsInvoke() || len(common.Args) == 0 {
					continue
				}
				sink, recognized := shadows.sinks[instruction]
				if !recognized || !sink.Certified {
					continue
				}
				parameter, ok := common.Args[0].(*ssa.Parameter)
				if !ok || parameter.Parent() != fn || !coroWorkerUintptrType(parameter.Type()) {
					continue
				}
				index := -1
				for candidate, exact := range fn.Params {
					if exact == parameter {
						index = candidate
						break
					}
				}
				if index >= 0 {
					sinks[instruction] = index
				}
			}
		}
	}
	if len(sinks) == 0 || len(returns) == 0 {
		return projection, false, nil
	}

	resultCount := fn.Signature.Results().Len()
	if resultCount > coroWorkerResultProjectionWidthV1 {
		resultCount = coroWorkerResultProjectionWidthV1
	}
	haveMapping := false
	for result := 0; result < resultCount; result++ {
		workerResult := -1
		functionParameter := -1
		exact := true
		for _, ret := range returns {
			if result >= len(ret.Results) {
				exact = false
				break
			}
			origin, ok := inferCoroWorkerResultOrigin(
				ret.Results[result], sinks, make(map[ssa.Value]bool),
			)
			if !ok {
				exact = false
				break
			}
			if workerResult < 0 {
				workerResult = origin.workerResult
				functionParameter = origin.functionParameter
				continue
			}
			if workerResult != origin.workerResult ||
				functionParameter != origin.functionParameter {
				exact = false
				break
			}
		}
		if !exact || workerResult < 0 {
			continue
		}
		if projection.functionParameter >= 0 &&
			projection.functionParameter != functionParameter {
			return emptyCoroWorkerResultProjection(), false, nil
		}
		projection.functionParameter = functionParameter
		projection.resultToWorker[result] = int8(workerResult)
		haveMapping = true
	}
	if !haveMapping {
		return emptyCoroWorkerResultProjection(), false, nil
	}
	projection.canonical = coroWorkerResultProjectionCanonical(projection)
	return projection, true, nil
}

func coroWorkerResultProjectionProvesAssertion(
	inferred, asserted coroWorkerResultProjection,
) bool {
	if inferred.functionParameter != asserted.functionParameter {
		return false
	}
	for result, worker := range asserted.resultToWorker {
		if worker >= 0 && inferred.resultToWorker[result] != worker {
			return false
		}
	}
	return true
}

// freezeCoroWorkerResultProjectionCertificates derives wrapper result
// forwarding from exact SSA. A legacy directive is accepted only as an
// assertion of the same derived fact; it can never manufacture a mapping that
// the body does not prove.
func (u *EmissionUniverse) freezeCoroWorkerResultProjectionCertificates() error {
	if u == nil || !u.CoroWorkerSupported() {
		return nil
	}
	shadows, err := AnalyzeCoroCallableShadows(u)
	if err != nil {
		return fmt.Errorf(
			"prepare emission universe: infer worker result projections from callable shadows: %w",
			err,
		)
	}
	for _, fn := range u.functions {
		if fn == nil || u.canonicalAlias(fn) != fn {
			continue
		}
		annotated, annotationPresent, err := coroWorkerResultProjectionFor(fn)
		if err != nil {
			return fmt.Errorf("prepare emission universe: worker result projection on %q: %w", fn.Name(), err)
		}
		projection, inferred, err := inferCoroWorkerResultProjection(shadows, fn)
		if err != nil {
			return fmt.Errorf("prepare emission universe: infer worker result projection on %q: %w", fn.Name(), err)
		}
		if annotationPresent {
			if !inferred || !coroWorkerResultProjectionProvesAssertion(projection, annotated) {
				return fmt.Errorf(
					"prepare emission universe: worker result projection %q is not proved by its exact SSA body",
					fn.Name(),
				)
			}
		}
		if !inferred {
			continue
		}
		if fn.Parent() != nil || len(fn.FreeVars) != 0 || len(fn.Blocks) == 0 || fn.Signature == nil ||
			fn.Signature.Recv() != nil || fn.Signature.Variadic() || fn.TypeParams() != nil || len(fn.TypeArgs()) != 0 {
			if annotationPresent {
				return fmt.Errorf("prepare emission universe: worker result projection %q requires an exact static non-generic Go wrapper", fn.Name())
			}
			// Automatic discovery is intentionally broader than certificate
			// publication. A standard-library wrapper may locally forward a
			// result while still being a method, variadic, generic, nested, or
			// otherwise open. Such a function receives no capability; only a
			// legacy assertion makes the unsupported shape an error.
			continue
		}
		params, results := fn.Signature.Params(), fn.Signature.Results()
		if params == nil || projection.functionParameter >= params.Len() ||
			projection.functionParameter >= len(fn.Params) ||
			!coroWorkerUintptrType(params.At(projection.functionParameter).Type()) ||
			!coroWorkerUintptrType(fn.Params[projection.functionParameter].Type()) {
			return fmt.Errorf("prepare emission universe: worker result projection %q function parameter %d is not uintptr-shaped", fn.Name(), projection.functionParameter)
		}
		for wrapper, worker := range projection.resultToWorker {
			if worker < 0 {
				continue
			}
			if results == nil || wrapper >= results.Len() || !coroWorkerUintptrType(results.At(wrapper).Type()) {
				return fmt.Errorf("prepare emission universe: worker result projection %q result %s is not a uintptr-shaped wrapper result", fn.Name(), coroWorkerResultWord(wrapper))
			}
		}
		identity := u.linkIdentities[fn]
		if identity == "" {
			return fmt.Errorf("prepare emission universe: worker result projection %q has no frozen function identity", fn.Name())
		}
		certificate := coroWorkerResultProjectionCertificate{
			functionParameter: projection.functionParameter,
			resultToWorker:    projection.resultToWorker,
		}
		certificate.id = framedEmissionKey(
			"llgo-coro-worker-result-projection-v1",
			identity,
			u.emissionTypeKeys.goLinknameABI(fn.Signature),
			projection.canonical,
		)
		u.workerResultProjections[fn] = certificate
	}
	return nil
}
