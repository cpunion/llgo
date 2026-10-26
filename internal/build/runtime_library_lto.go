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
	"github.com/xgo-dev/llgo/internal/optlevel"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"os"
	"runtime"
)

const runtimeLibraryOptimizationVersion = "darwin-arm64-runtime-o2-lto-target-v2"

// Only the three private implementation modules enter this library optimizer.
// Application and standard-library packages keep their native Os objects, so
// large user coroutine modules never enter the combined linker IR. Keep the
// measured native profile explicit; cross targets, debug builds and custom
// linker/archive choices retain the ordinary package pipeline.
func runtimeLibraryLTO(conf *Config) bool {
	return conf != nil && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" &&
		conf.Mode != ModeGen && !conf.PackageCompileOnly && conf.BuildMode == BuildModeExe &&
		conf.Goos == "darwin" && conf.Goarch == "arm64" && conf.Target == "" &&
		(conf.GOARM64 == "" || conf.GOARM64 == "v8.0") &&
		conf.OptLevel == optlevel.Os && !conf.ltoEnabled() && !conf.deadcodeDropEnabled() &&
		!conf.GenLL && !conf.CheckLLFiles && !conf.ForceEspClang &&
		conf.LinkOptions.DWARF != DWARFPreserve &&
		conf.LinkOptions.ExternalLinker == "" && conf.LinkOptions.ExternalLinkerFlags == "" &&
		os.Getenv("LLGO_AR") == ""
}

func runtimeLibraryLTOPackage(conf *Config, pkg string) bool {
	if !runtimeLibraryLTO(conf) {
		return false
	}
	switch pkg {
	case "github.com/xgo-dev/llgo/runtime/internal/coro",
		"github.com/xgo-dev/llgo/runtime/internal/runtime",
		"github.com/xgo-dev/llgo/runtime/internal/coroalloc":
		return true
	}
	return false
}

func runtimeLibraryOptLevel(conf *Config, pkg string) optlevel.Level {
	if runtimeLibraryLTOPackage(conf, pkg) {
		return optlevel.O2
	}
	return conf.OptLevel
}

func runtimeLibraryOptimizationID(conf *Config) string {
	if runtimeLibraryLTO(conf) {
		return runtimeLibraryOptimizationVersion
	}
	return ""
}

// prepareRuntimeLibraryTargetAttributes preserves the exact effective backend
// configuration across full-LTO serialization. A TargetMachine's options do
// not become function attributes merely by writing its module as bitcode.
// Check every definition first so a conflicting foreign attribute cannot leave
// a partially changed module behind. Declarations retain their own contracts.
func prepareRuntimeLibraryTargetAttributes(mod llvm.Module, target llssa.TargetSpec) error {
	if mod.IsNil() || target.Triple == "" {
		return fmt.Errorf("runtime library target attributes: missing module or effective target")
	}
	if mod.Target() != target.Triple {
		return fmt.Errorf("runtime library target attributes: module target %q differs from effective target %q", mod.Target(), target.Triple)
	}
	// Explicit toolchain profiles may defer CPU or features to their linker.
	// Keep those legacy modules unchanged rather than invent a frozen option.
	if target.CPU == "" || target.Features == "" {
		return nil
	}
	settings := []struct{ name, value string }{
		{"target-cpu", target.CPU},
		{"target-features", target.Features},
	}
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		for _, setting := range settings {
			attribute := fn.GetStringAttributeAtIndex(-1, setting.name)
			if !attribute.IsNil() && attribute.GetStringValue() != setting.value {
				return fmt.Errorf("runtime library target attributes: %s has conflicting %s=%q, want %q", fn.Name(), setting.name, attribute.GetStringValue(), setting.value)
			}
		}
	}
	ctx := mod.Context()
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		for _, setting := range settings {
			fn.AddFunctionAttr(ctx.CreateStringAttribute(setting.name, setting.value))
		}
	}
	return nil
}
