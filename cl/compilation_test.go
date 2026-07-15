//go:build !llgo
// +build !llgo

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
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

func TestCompilationCoroPlanObservationAndCacheRegistration(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F() int { return 42 }
`)
	plan := new(coro.SSAPlan)
	observerCalls := 0
	compilation := &Compilation{
		CoroPlan: plan,
		CoroPlanObserver: func(pkg *ssa.Package, got *coro.SSAPlan) {
			observerCalls++
			if pkg != ssaPkg {
				t.Errorf("observer package = %p, want %p", pkg, ssaPkg)
			}
			if got != plan {
				t.Errorf("observer plan = %p, want %p", got, plan)
			}
		},
	}

	compile := func(cacheHit bool) string {
		t.Helper()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: compilation,
			CacheHit:    cacheHit,
		})
		if err != nil {
			t.Fatalf("NewPackageExWithEmbedOptions(cache hit %v): %v", cacheHit, err)
		}
		return pkg.String()
	}

	sourceIR := compile(false)
	if observerCalls != 1 {
		t.Fatalf("source observer calls = %d, want 1", observerCalls)
	}
	cachedIR := compile(true)
	if observerCalls != 1 {
		t.Fatalf("cache registration observer calls = %d, want unchanged 1", observerCalls)
	}
	if cachedIR != sourceIR {
		t.Fatal("cache registration option changed frontend LLVM IR")
	}
}
