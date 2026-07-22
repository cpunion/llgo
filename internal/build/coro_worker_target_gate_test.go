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
	"strings"
	"testing"
)

func TestCoroWorkerRequiresNativeRuntimeAdapter(t *testing.T) {
	base := Config{
		BuildMode:                     BuildModeExe,
		Goos:                          "linux",
		Goarch:                        "amd64",
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapABI: true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroWorker:              true,
	}
	if err := validateCoroProgramBootstrapConfig(&base); err != nil {
		t.Fatalf("native worker adapter rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "wasm", mutate: func(conf *Config) { conf.Goos, conf.Goarch = "wasip1", "wasm" }},
		{name: "baremetal", mutate: func(conf *Config) { conf.Tags = "baremetal" }},
		{name: "host pull", mutate: func(conf *Config) { conf.Tags = "llgo_coro_host" }},
		{name: "named target", mutate: func(conf *Config) { conf.Target = "rp2040" }},
		{name: "unsupported os", mutate: func(conf *Config) { conf.Goos = "windows" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := base
			test.mutate(&conf)
			err := validateCoroProgramBootstrapConfig(&conf)
			if err == nil || !strings.Contains(err.Error(), "native Darwin/Linux pthread worker adapter") {
				t.Fatalf("target gate error = %v", err)
			}
		})
	}
}

func TestCoroNativeFleetRequiresCompleteNativeRuntime(t *testing.T) {
	base := Config{
		BuildMode:                     BuildModeExe,
		Goos:                          "linux",
		Goarch:                        "amd64",
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapABI: true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroWorker:              true,
		EnableCoroNativeFleet:         true,
	}
	if err := validateCoroProgramBootstrapConfig(&base); err != nil {
		t.Fatalf("native fleet rejected: %v", err)
	}

	for _, test := range []struct {
		name string
		want string
		set  func(*Config)
	}{
		{name: "program", want: "runnable program bootstrap", set: func(conf *Config) {
			conf.EnableCoroProgramBootstrapRun = false
		}},
		{name: "worker", want: "bounded native worker capability", set: func(conf *Config) {
			conf.EnableCoroWorker = false
		}},
		{name: "32-bit", want: "64-bit native Darwin/Linux timer reactor", set: func(conf *Config) {
			conf.Goarch = "arm"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := base
			test.set(&conf)
			err := validateCoroProgramBootstrapConfig(&conf)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("native fleet target gate error = %v, want %q", err, test.want)
			}
		})
	}
}
