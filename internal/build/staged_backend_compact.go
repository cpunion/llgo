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
	"os"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// After pruning a large lowered module, reload its smaller IR to release
// context-wide uniquing tables left by deleted coroutine scaffolding. Allocator
// relief alone cannot release these live tables. Small packages avoid the extra
// bitcode I/O. The complete requested optimization pipeline follows the reload.
const largeStagedPackageBitcodeSize = 64 << 20

// compactLoweredCoroPackage transfers ownership, including on error. Before
// serialization succeeds it returns the original live Program; afterwards it
// returns a new Program owning the parsed module. Callers must dispose the
// returned Program, rather than a deferred copy of the original owner.
func compactLoweredCoroPackage(backend llssa.Program, mod llvm.Module, pkgPath string, discardLocalNames bool) (llssa.Program, llvm.Module, error) {
	if backend == nil || backend.Target() == nil || mod.IsNil() {
		return backend, mod, fmt.Errorf("compact package %s before optimization: missing program or module", pkgPath)
	}
	file, err := os.CreateTemp("", "llgo-compact-*.bc")
	if err != nil {
		return backend, mod, fmt.Errorf("stage package %s before optimization: %w", pkgPath, err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := llvm.WriteBitcodeToFile(mod, file); err != nil {
		return backend, mod, fmt.Errorf("write package %s before optimization: %w", pkgPath, err)
	}
	if err := file.Close(); err != nil {
		return backend, mod, fmt.Errorf("close package %s before optimization: %w", pkgPath, err)
	}
	target := *backend.Target()
	if target.Resolved != nil {
		resolved := *target.Resolved
		target.Resolved = &resolved
	}
	backend.Dispose()
	releaseNativeHeap()
	backend = llssa.NewProgram(&target)
	if discardLocalNames {
		backend.DiscardLocalValueNames()
	}
	mod, err = backend.ParseBitcodeFile(file.Name())
	if err != nil {
		return backend, mod, fmt.Errorf("reload package %s before optimization: %w", pkgPath, err)
	}
	return backend, mod, nil
}
