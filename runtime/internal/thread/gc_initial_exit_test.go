//go:build (darwin || linux) && !llgo

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

package thread

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectorContinuesAfterInitialThreadExit(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	config, err := exec.LookPath("pkg-config")
	if err != nil {
		t.Skip("pkg-config is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flags, err := exec.CommandContext(ctx, config, "--cflags", "--libs", "bdw-gc").Output()
	if err != nil {
		t.Skip("BDWGC development files are unavailable")
	}
	binary := filepath.Join(t.TempDir(), "gc-initial-exit")
	args := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", "-pthread",
		filepath.Join("testdata", "gc_initial_exit.c"),
		filepath.Join("..", "clite", "bdwgc", "_wrap", "coro_initial_exit.c")}
	args = append(args, strings.Fields(string(flags))...)
	args = append(args, "-o", binary)
	if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile initial collector thread regression: %v\n%s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("collect after initial thread exit: %v\n%s", err, output)
	} else if !strings.Contains(string(output), "PASS collection after original GC thread exit") {
		t.Fatalf("missing post-join collection witness: %s", output)
	}
}
