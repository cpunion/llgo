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
	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llgo/internal/lto"
	"github.com/xgo-dev/llgo/internal/optlevel"
)

func nativeFullLTOAlignmentArgs(conf *Config, target *crosscompile.Export) []string {
	if conf == nil || target == nil || conf.Goos != "linux" || conf.Goarch != "amd64" ||
		conf.BuildMode != BuildModeExe || conf.OptLevel != optlevel.O3 || conf.ltoMode() != lto.Full {
		return nil
	}
	if target.Toolchain.ObjectFormat != crosscompile.ObjectFormatELF ||
		target.Toolchain.Linker != crosscompile.LinkerFlavorELFLLD ||
		conf.LinkOptions.ExternalLinker != "" || target.Linker != "" {
		return nil
	}
	// This speed-oriented profile trades padding for stable entry phases and
	// stronger loop alignment. Apply both preferences at the final LTO backend;
	// per-package object alignment cannot constrain its regenerated code.
	return []string{
		"-Wl,-mllvm,-align-all-functions=6",
		"-Wl,-mllvm,-align-loops=32",
	}
}
