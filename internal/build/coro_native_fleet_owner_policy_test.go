//go:build !llgo && (darwin || linux)

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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoroNativeInitialExecutionLimitPolicy(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ownerDir := filepath.Join("..", "..", "runtime", "internal", "corofleet", "_owner")
	temp := t.TempDir()
	driver := filepath.Join(temp, "owner-count.c")
	const source = `#include "owner.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t route) {
    return route != 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        return 2;
    }
    unsigned long maximum = strtoul(argv[1], 0, 10);
    printf("%u\n", __llgo_coro_fleet_owner_count_v1((uint32_t)maximum));
    return 0;
}
`
	if err := os.WriteFile(driver, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(temp, "owner-count")
	ownerSource := filepath.Join(ownerDir, "owner.c")
	if output, err := exec.Command(
		clang,
		"-std=c11",
		"-Wall",
		"-Wextra",
		"-Werror",
		"-pthread",
		"-I",
		ownerDir,
		driver,
		ownerSource,
		"-o",
		executable,
	).CombinedOutput(); err != nil {
		t.Fatalf("compile native initial execution-limit policy: %v\n%s", err, output)
	}

	run := func(maximum string, gomaxprocs *string) uint64 {
		t.Helper()
		command := exec.Command(executable, maximum)
		command.Env = make([]string, 0, len(os.Environ())+1)
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "GOMAXPROCS=") {
				command.Env = append(command.Env, item)
			}
		}
		if gomaxprocs != nil {
			command.Env = append(command.Env, "GOMAXPROCS="+*gomaxprocs)
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run native initial execution-limit policy: %v\n%s", err, output)
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 32)
		if err != nil {
			t.Fatalf("parse native initial execution limit %q: %v", output, err)
		}
		return value
	}

	value := func(text string) *string {
		return &text
	}
	if got := run("0", value("4")); got != 0 {
		t.Fatalf("zero maximum selected execution limit %d", got)
	}
	for _, test := range []struct {
		name       string
		maximum    string
		gomaxprocs *string
		want       uint64
	}{
		{name: "one", maximum: "8", gomaxprocs: value("1"), want: 1},
		{name: "four", maximum: "8", gomaxprocs: value("4"), want: 4},
		{name: "clamp", maximum: "8", gomaxprocs: value("9"), want: 8},
		{name: "overflow", maximum: "4294967295", gomaxprocs: value("429496729600000000000"), want: 4294967295},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := run(test.maximum, test.gomaxprocs); got != test.want {
				t.Fatalf("initial execution limit = %d, want %d", got, test.want)
			}
		})
	}

	fallback := run("4294967295", nil)
	if fallback == 0 {
		t.Fatal("online CPU fallback selected zero execution limit")
	}
	for _, invalid := range []string{"", "0", "+4", "429496729600000000000x"} {
		t.Run("fallback-"+invalid, func(t *testing.T) {
			if got := run("4294967295", value(invalid)); got != fallback {
				t.Fatalf("invalid GOMAXPROCS %q selected limit %d, want fallback %d", invalid, got, fallback)
			}
		})
	}
}
