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
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroPhysicalEmissionSessionCommitsOneCompleteBody(t *testing.T) {
	ctx := &context{}
	plan := &coroPhysicalFunctionPlan{}
	session, finish := ctx.beginCoroPhysicalEmission(plan, 3, true)
	if !ctx.hasCoroPhysicalEmission() || ctx.hasCoroPhysicalBody() {
		t.Fatal("prologue must expose the session but not a partial physical body")
	}
	if got := ctx.coroEmissionPlan(); got != plan {
		t.Fatalf("prologue plan = %p, want %p", got, plan)
	}
	if got := ctx.coroEmissionSourceParamBase(); got != 3 {
		t.Fatalf("physical parameter base = %d, want 3", got)
	}
	if !ctx.coroEmissionExplicitStatus() {
		t.Fatal("explicit-status capability was not frozen in the session")
	}

	body := &coroBodyContext{}
	blocks := make([]llssa.BasicBlock, 1)
	session.bindCoroPhysicalBody(body, blocks)
	if got := ctx.coroBody(); got != body {
		t.Fatalf("bound body = %p, want %p", got, body)
	}
	if _, ok := ctx.coroEmissionSourceBlock(0); !ok {
		t.Fatal("bound source-block projection is not visible with the body")
	}
	if message := captureCoroEmissionSessionPanic(func() {
		session.bindCoroPhysicalBody(&coroBodyContext{}, blocks)
	}); !strings.Contains(message, "exactly once") {
		t.Fatalf("second body bind panic = %q", message)
	}

	session.site = &coroSiteEmissionObserver{}
	if message := captureCoroEmissionSessionPanic(func() {
		session.completeCoroPhysicalBody(body)
	}); !strings.Contains(message, "no active source SitePlan") {
		t.Fatalf("completion with active SitePlan panic = %q", message)
	}
	session.site = nil
	session.completeCoroPhysicalBody(body)
	if ctx.hasCoroPhysicalBody() {
		t.Fatal("completed body remained available to ordinary lowering")
	}
	finish()
	if ctx.hasCoroPhysicalEmission() {
		t.Fatal("completed session remained installed")
	}
}

func TestCoroPhysicalEmissionSessionRejectsPartialAndNestedState(t *testing.T) {
	ctx := &context{}
	plan := &coroPhysicalFunctionPlan{}
	session, finish := ctx.beginCoroPhysicalEmission(plan, 2, false)
	if message := captureCoroEmissionSessionPanic(func() {
		ctx.beginCoroPhysicalEmission(plan, 2, false)
	}); !strings.Contains(message, "nested") {
		t.Fatalf("nested session panic = %q", message)
	}
	if message := captureCoroEmissionSessionPanic(finish); !strings.Contains(message, "closed in phase") {
		t.Fatalf("partial close panic = %q", message)
	}
	if ctx.hasCoroPhysicalEmission() {
		t.Fatal("failed partial close did not clear the installed session")
	}
	if session.phase != coroPhysicalEmissionPrologue {
		t.Fatalf("failed partial session phase = %d, want prologue", session.phase)
	}
}

func TestCoroPhysicalEmissionSessionPreservesEmissionPanicAndClearsState(t *testing.T) {
	ctx := &context{}
	message := captureCoroEmissionSessionPanic(func() {
		_, finish := ctx.beginCoroPhysicalEmission(&coroPhysicalFunctionPlan{}, 2, false)
		defer finish()
		panic("sentinel emission failure")
	})
	if message != "sentinel emission failure" {
		t.Fatalf("emission panic = %q", message)
	}
	if ctx.hasCoroPhysicalEmission() {
		t.Fatal("panicking emission left a partial session installed")
	}
}

func captureCoroEmissionSessionPanic(run func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	run()
	return ""
}
