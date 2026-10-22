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
	"github.com/xgo-dev/llgo/internal/coro"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoroIONilComparisonHasNoDynamicEqualityEdge(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	stop := errors.New("diagnostic stopped after exact whole-program plan; no code emission")
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := defaultCoroPlanBuilder(input)
		if err != nil {
			return nil, err
		}
		found := false
		for _, item := range plan.Functions() {
			if item.Function == nil || item.Function.String() != "(*internal/poll.FD).eofError" {
				continue
			}
			found = true
			for _, call := range plan.LoweredCalls(item.Function) {
				if call.LogicalName == "EfaceEqual" || call.LogicalName == "IfaceType" {
					t.Errorf("nil-only eofError retained dynamic helper %s and effect %s", call.LogicalName, item.Plan.Effect)
				}
			}
			if !item.Plan.HasStaticOutcome() {
				t.Errorf("nil-only eofError lacks synchronous outcome: %+v", item.Plan)
			}
		}
		if !found {
			t.Fatal("real eofError absent from plan")
		}
		return nil, stop
	}
	_, err := Do([]string{filepath.Join("..", "..", "benchmark", "coro_core", "testdata", "io_workload", "main.go")}, conf)
	if err == nil || !strings.Contains(err.Error(), stop.Error()) {
		t.Fatalf("unexpected diagnostic result: %v", err)
	}
}
