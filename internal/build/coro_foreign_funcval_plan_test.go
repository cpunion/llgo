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
	"errors"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDefaultForeignFunctionValueUsesSuspendingAdapter(t *testing.T) {
	checked := errors.New("foreign function-value adapter checked")
	conf := NewDefaultConf(ModeBuild)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := defaultCoroPlanBuilder(input)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entry := range plan.Functions() {
			fn := entry.Function
			if fn == nil || !strings.Contains(fn.Name(), "foreign$wrapper$llgo$managed-foreign$") {
				continue
			}
			found = true
			function, ok := plan.FunctionPlan(fn)
			if !ok || !function.Effect.MaySuspend() || function.Emission != coro.EmitCoroutine {
				t.Errorf("default may-block C function value must publish a coroutine adapter, got %+v", function)
			}
			for _, caller := range plan.Functions() {
				if caller.Function.Name() != "invoke$1" {
					continue
				}
				for _, block := range caller.Function.Blocks {
					for _, instruction := range block.Instrs {
						call, ok := instruction.(*ssa.Call)
						if !ok || call.Common().StaticCallee() != nil {
							continue
						}
						callPlan, _ := plan.CallPlan(call)
						published := false
						for _, target := range callPlan.Targets {
							published = published || target == function.ID
						}
						if !published {
							t.Errorf("dynamic call omitted published managed foreign adapter: %+v", callPlan)
						}
					}
				}
			}
		}
		if !found {
			t.Error("no managed foreign function-value adapter was materialized")
		}
		return nil, checked
	}
	_, err := Do([]string{"./_testgo/coro_foreign_funcval"}, conf)
	if !errors.Is(err, checked) {
		t.Fatalf("prepare foreign function-value plan: %v", err)
	}
}
