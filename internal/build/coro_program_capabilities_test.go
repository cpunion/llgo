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
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestImportedProgramCapabilitiesDigestIsCanonicalAndCapabilitySensitive(t *testing.T) {
	alpha, beta := new(ssa.Function), new(ssa.Function)
	alphaFact := coro.LibraryEffectFunction{
		ID:                  "llgo.function.v0:alpha",
		ProgramCapabilities: coro.NewProgramCapabilities(true, false),
	}
	betaFact := coro.LibraryEffectFunction{
		ID:                  "llgo.function.v0:beta",
		ProgramCapabilities: coro.NewProgramCapabilities(false, true),
	}
	first, err := coroImportedProgramCapabilitiesDigest(map[*ssa.Function]coro.LibraryEffectFunction{
		alpha: alphaFact,
		beta:  betaFact,
	})
	if err != nil || first == "" {
		t.Fatalf("first imported capability digest = %q, %v", first, err)
	}
	second, err := coroImportedProgramCapabilitiesDigest(map[*ssa.Function]coro.LibraryEffectFunction{
		beta:  betaFact,
		alpha: alphaFact,
	})
	if err != nil || second != first {
		t.Fatalf("reordered imported capability digest = %q, %v; want %q", second, err, first)
	}

	alphaFact.ProgramCapabilities = coro.NewProgramCapabilities(true, true)
	changed, err := coroImportedProgramCapabilitiesDigest(map[*ssa.Function]coro.LibraryEffectFunction{
		alpha: alphaFact,
		beta:  betaFact,
	})
	if err != nil || changed == first {
		t.Fatalf("changed imported capability digest = %q, %v; baseline %q", changed, err, first)
	}
	zero, err := coroImportedProgramCapabilitiesDigest(map[*ssa.Function]coro.LibraryEffectFunction{
		alpha: {ID: alphaFact.ID},
	})
	if err != nil || zero != "" {
		t.Fatalf("zero imported capability digest = %q, %v", zero, err)
	}
}
