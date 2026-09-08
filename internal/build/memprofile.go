/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package build

import (
	"slices"

	"github.com/xgo-dev/llgo/cl"
)

func (c *context) configureWasmMemoryProfiling(linearGC bool) {
	conf := c.buildConf
	enabled := false
	tags := splitSourcePatchBuildTags(conf.Tags)
	if linearGC && usesSingleWorkerWasmScheduler(conf) &&
		!slices.Contains(tags, "llgo.wasi_threads") && !slices.Contains(tags, "llgo.wasm.workers") {
		// A library's future callers are unknown. Executables can prove the
		// profiling API unused and omit both sampling and frame attribution.
		enabled = conf.BuildMode != BuildModeExe || cl.MemProfileConsumer(c.progSSA.AllPackages()) != ""
	}
	c.prog.EnableWasmMemoryProfiling(enabled)
	c.callerTracking.SetMemoryProfileAttribution(c.prog.WasmMemoryProfilingEnabled())
}
