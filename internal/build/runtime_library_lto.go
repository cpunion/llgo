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
	"github.com/xgo-dev/llgo/internal/optlevel"
	"os"
	"runtime"
)

const runtimeLibraryOptimizationVersion = "darwin-arm64-runtime-o2-public-runtime-os-lto-v1"

// The three private implementation modules use O2. The public runtime package
// contributes Os bitcode to the same linker so its wrappers can be optimized
// with their implementation. Application and other standard-library packages
// keep native Os objects and do not enter the combined linker IR. Keep the
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
	case "runtime",
		"github.com/xgo-dev/llgo/runtime/internal/coro",
		"github.com/xgo-dev/llgo/runtime/internal/runtime",
		"github.com/xgo-dev/llgo/runtime/internal/coroalloc":
		return true
	}
	return false
}

func runtimeLibraryOptLevel(conf *Config, pkg string) optlevel.Level {
	if pkg != "runtime" && runtimeLibraryLTOPackage(conf, pkg) {
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
