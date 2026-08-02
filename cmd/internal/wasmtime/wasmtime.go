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

// Package wasmtime locates the Wasmtime guest-debug server used by llgo debug.
package wasmtime

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Version 44 is the first Wasmtime release containing the built-in gdbstub
// guest-debug frontend used by LLGo.
const MinimumGuestDebugVersion = 44

var versionPattern = regexp.MustCompile(`(?im)^wasmtime\s+([0-9]+)(?:\.[0-9]+){1,2}\b`)

// Find returns a validated Wasmtime executable. An explicit path takes
// precedence over LLGO_WASMTIME and PATH.
func Find(configuredPath string) (string, error) {
	return findFrom(configuredPath, os.Getenv("LLGO_WASMTIME"), []string{"wasmtime"})
}

func findFrom(configuredPath, environmentPath string, candidates []string) (string, error) {
	if configuredPath != "" {
		return validate(configuredPath)
	}
	if environmentPath != "" {
		return validate(environmentPath)
	}
	for _, candidate := range candidates {
		path, err := validate(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("llgo debug: Wasmtime %d or newer with the built-in gdbstub was not found; install Wasmtime or set LLGO_WASMTIME", MinimumGuestDebugVersion)
}

func validate(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("llgo debug: find Wasmtime %q: %w", name, err)
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("llgo debug: query Wasmtime %q version: %w", path, err)
	}
	match := versionPattern.FindStringSubmatch(string(output))
	if len(match) != 2 {
		return "", fmt.Errorf("llgo debug: cannot parse Wasmtime version from %q", strings.TrimSpace(string(output)))
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major < MinimumGuestDebugVersion {
		return "", fmt.Errorf("llgo debug: %q is Wasmtime %s; version %d or newer with the built-in gdbstub is required", path, match[1], MinimumGuestDebugVersion)
	}
	return path, nil
}
