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

import "testing"

func TestCoroWorkerCapabilityMatchesNativeRuntimeAdapter(t *testing.T) {
	base := Config{
		BuildMode: BuildModeExe,
		Goos:      "linux",
		Goarch:    "amd64"}
	if err := validateCoroProgramBootstrapConfig(&base); err != nil {
		t.Fatalf("native worker adapter rejected: %v", err)
	}
	if !base.coroWorkerSupported() {
		t.Fatal("native stackless target did not select its worker adapter")
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
			if conf.coroWorkerSupported() {
				t.Fatal("target without a native worker adapter advertised worker capability")
			}
			if err := validateCoroProgramBootstrapConfig(&conf); err != nil {
				t.Fatalf("portable stackless target was rejected: %v", err)
			}
		})
	}
}

func TestCoroNativeFleetCapabilityMatchesCompleteNativeRuntime(t *testing.T) {
	base := Config{
		BuildMode: BuildModeExe,
		Goos:      "linux",
		Goarch:    "amd64"}
	if err := validateCoroProgramBootstrapConfig(&base); err != nil {
		t.Fatalf("native fleet rejected: %v", err)
	}
	if !base.coroNativeFleetSupported() {
		t.Fatal("native stackless target did not select its executor fleet")
	}

	for _, test := range []struct {
		name string
		set  func(*Config)
	}{
		{name: "32-bit", set: func(conf *Config) {
			conf.Goarch = "arm"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := base
			test.set(&conf)
			if !conf.coroWorkerSupported() {
				t.Fatal("32-bit POSIX target lost its bounded pthread worker capability")
			}
			if conf.coroNativeFleetSupported() {
				t.Fatal("target without the complete native reactor advertised fleet capability")
			}
			if err := validateCoroProgramBootstrapConfig(&conf); err != nil {
				t.Fatalf("non-fleet stackless configuration was rejected: %v", err)
			}
		})
	}
}
