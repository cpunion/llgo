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

func parseCoroWorkerResultProjectionDecl(decl *ast.FuncDecl) (coroWorkerResultProjection, bool, error) {
	projection := coroWorkerResultProjection{functionParameter: -1}
	for index := range projection.resultToWorker {
		projection.resultToWorker[index] = -1
	}
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

// freezeCoroWorkerResultProjectionCertificates validates every annotation even
// when its wrapper is not reached by a currently certified worker sink. This
// keeps malformed trusted metadata from silently becoming active after an
// unrelated reachability change.
func (u *EmissionUniverse) freezeCoroWorkerResultProjectionCertificates() error {
	if u == nil || !u.CoroWorkerEnabled() {
		return nil
	}
	for _, fn := range u.functions {
		if fn == nil || u.canonicalAlias(fn) != fn {
			continue
		}
		projection, present, err := coroWorkerResultProjectionFor(fn)
		if err != nil {
			return fmt.Errorf("prepare emission universe: worker result projection on %q: %w", fn.Name(), err)
		}
		if !present {
			continue
		}
		if fn.Parent() != nil || len(fn.FreeVars) != 0 || len(fn.Blocks) == 0 || fn.Signature == nil ||
			fn.Signature.Recv() != nil || fn.Signature.Variadic() || fn.TypeParams() != nil || len(fn.TypeArgs()) != 0 {
			return fmt.Errorf("prepare emission universe: worker result projection %q requires an exact static non-generic Go wrapper", fn.Name())
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
			structuralGoLinknameABITypeKey(fn.Signature),
			projection.canonical,
		)
		u.workerResultProjections[fn] = certificate
	}
	return nil
}
