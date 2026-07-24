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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoroHardCutoverRejectsLegacyProductionSurface is a zero-tolerance gate,
// not a debt snapshot. Once the stackless architecture crossed this boundary there
// is only one logical Park protocol, one current Timer/Poll ABI, and no staged
// EnableCoro feature surface. Reintroducing an old spelling therefore fails in
// the same commit instead of silently creating a second runtime track.
func TestCoroHardCutoverRejectsLegacyProductionSurface(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	forbidden := []string{
		"RuntimeProfile",
		"CoroProfile",
		"CoroEntryResolutionActive(",
		"CoroPhysicalABIActive(",
		"CoroChildAwaitActive(",
		"CoroPlainDispatchActive(",
		"CoroClosedStaticSpawnActive(",
		"CoroProgramBootstrapActive(",
		"CoroChannelActive(",
		"CoroExplicitStatusActive(",
		"coroEntryResolutionActive(",
		"coroPhysicalABIActive(",
		"coroChildAwaitActive(",
		"coroPlainDispatchActive(",
		"coroClosedStaticSpawnActive(",
		"coroProgramBootstrapABIActive(",
		"coroProgramBootstrapActive(",
		"coroChannelActive(",
		"coroExplicitStatusActive(",
		"enableClosedStaticSpawn",
		"enableManagedDispatch",
		"outcomeMode",
		"WaitToken",
		"WaitRegistration",
		"KeyedWait",
		"PostWaitAndRequest",
		"FrameRetentionTimerABIV1",
		"CoroFrameRetentionTimerABIV1",
		"timerRegistrationModeV1",
		"pollOperationModeV1",
		"EnableCoro",
		"ProgramBootstrapVersionV1",
		"ProgramStepFlagInitV1",
		"ProgramStepFlagMainV1",
		"ValidateProgramDescriptorV1",
		"ValidateRunnableDirectProgramV1",
		"coroProgramBootstrapVersionV1",
		"coroProgramBootstrapFactorySymbolV1",
		"selectCoroProgramBootstrapV1",
		"emitCoroProgramBootstrapFactoryV1",
		"__llgo_coro_program_bootstrap_v1",
		"__llgo_coro_program_bootstrap_factory_v1",
		"__llgo_coro_wait_prepare_v1",
		"__llgo_coro_wait_rollback_v1",
		"__llgo_coro_wait_retire_v1",
		"__llgo_coro_native_post_wait_v1",
		"__llgo_coro_host_post_wait_v1",
		"__llgo_coro_park_prepare_v1",
		"__llgo_coro_timer_prepare_after_v1",
		"__llgo_coro_timer_retire_completed_v1",
		"__llgo_coro_sema_prepare_or_abort_v1",
		"__llgo_coro_sema_retire_completed_or_abort_v1",
		"__llgo_coro_sema_release_or_abort_v1",
		"__llgo_coro_notify_prepare_or_abort_v1",
		"__llgo_coro_notify_retire_completed_or_abort_v1",
		"__llgo_coro_notify_one_or_abort_v1",
		"__llgo_coro_notify_all_or_abort_v1",
	}

	for _, root := range []string{"cl", "internal/build", "internal/coro", "runtime"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			source := string(contents)
			for _, spelling := range forbidden {
				if strings.Contains(source, spelling) {
					rel, relErr := filepath.Rel(repoRoot, path)
					if relErr != nil {
						return relErr
					}
					t.Errorf("legacy coroutine production surface %q remains in %s", spelling, filepath.ToSlash(rel))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
