//go:build darwin || linux

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

package corofleet

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeRetainedOwnerPreservesThreadAndDispatchGeneration(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "retained-owner")
	command := exec.CommandContext(ctx, cc, "-std=c11", "-O2", "-pthread", "-Wall", "-Wextra", "-Werror", "-I", "_owner", filepath.Join("testdata", "retained_owner.c"), filepath.Join("_owner", "owner.c"), "-o", binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile retained owner control: %v\n%s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("retained owner lifecycle/TLS control: %v\n%s", err, output)
	}
}
