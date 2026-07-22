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
	"runtime"
	"strings"

	llssa "github.com/goplus/llgo/ssa"
)

const coroWorkerNativeTargetRequirement = "native Darwin/Linux pthread worker adapter"

// validateCoroWorkerNativeProgramTarget mirrors the runtime implementation
// which currently owns the bounded worker queue. Worker lowering is a target
// capability, not a property of an otherwise shape-compatible call: WASM,
// named/embedded targets, and non-POSIX runtime profiles must supply their own
// transport before cl may emit the worker park/resume ABI for them.
func validateCoroWorkerNativeProgramTarget(prog llssa.Program) error {
	if prog == nil {
		return fmt.Errorf("coroutine worker lowering requires the %s: nil LLVM program", coroWorkerNativeTargetRequirement)
	}
	target := prog.Target()
	if target == nil {
		return fmt.Errorf("coroutine worker lowering requires the %s: missing target profile", coroWorkerNativeTargetRequirement)
	}
	if target.Target != "" {
		return fmt.Errorf(
			"coroutine worker lowering requires the %s: named target %q has no worker capability",
			coroWorkerNativeTargetRequirement, target.Target,
		)
	}

	goos := target.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := target.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goarch == "wasm" {
		return fmt.Errorf(
			"coroutine worker lowering requires the %s: GOARCH %q has no pthread worker capability",
			coroWorkerNativeTargetRequirement, goarch,
		)
	}
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf(
			"coroutine worker lowering requires the %s: GOOS %q is unsupported",
			coroWorkerNativeTargetRequirement, goos,
		)
	}

	// GOOS/GOARCH alone are not sufficient when a direct cl caller supplies an
	// authoritative LLVM target. Check both the immutable requested target and
	// the effective in-process target so a wasm or foreign-OS triple cannot be
	// hidden by the target-machine compatibility fallback.
	for _, item := range []struct {
		name   string
		triple string
	}{
		{name: "requested", triple: prog.RequestedTargetSpec().Triple},
		{name: "effective", triple: prog.TargetSpec().Triple},
	} {
		if !coroWorkerTripleMatchesNativeOS(item.triple, goos) {
			return fmt.Errorf(
				"coroutine worker lowering requires the %s: %s LLVM triple %q does not match GOOS %q",
				coroWorkerNativeTargetRequirement, item.name, item.triple, goos,
			)
		}
	}
	return nil
}

func coroWorkerTripleMatchesNativeOS(triple, goos string) bool {
	triple = strings.ToLower(strings.TrimSpace(triple))
	if triple == "" {
		return false
	}
	arch, _, _ := strings.Cut(triple, "-")
	if strings.HasPrefix(arch, "wasm") {
		return false
	}
	switch goos {
	case "darwin":
		return strings.Contains(triple, "-darwin") || strings.Contains(triple, "-macos")
	case "linux":
		return strings.Contains(triple, "-linux")
	default:
		return false
	}
}

// validateCoroWorkerUniverseTarget protects direct Compilation users which do
// not pass through internal/build's target gate.
func (c *Compilation) validateCoroWorkerUniverseTarget() error {
	if c == nil || !c.CoroWorkerActive() || c.EmissionUniverse == nil {
		return nil
	}
	return validateCoroWorkerNativeProgramTarget(c.EmissionUniverse.prog)
}

// validateCoroWorkerCodegenProgram binds the target-safe universe to the exact
// LLVM program used for package emission. Without this identity check a caller
// could prepare a native universe and then lower its worker operations into a
// WASM (or otherwise incompatible) program.
func (c *Compilation) validateCoroWorkerCodegenProgram(prog llssa.Program) error {
	if c == nil || !c.CoroWorkerActive() {
		return nil
	}
	if c.EmissionUniverse == nil {
		return fmt.Errorf("coroutine worker lowering requires a prepared emission universe")
	}
	if prog != c.EmissionUniverse.prog {
		return fmt.Errorf("coroutine worker lowering requires the exact LLVM program used to prepare its emission universe")
	}
	return validateCoroWorkerNativeProgramTarget(prog)
}
