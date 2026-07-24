//go:build baremetal

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

package c

import (
	_ "unsafe"
)

const (
	// we want to execute init(), link / decl skips executing init()
	LLGoPackage = true
)

// openBaremetalStandardStream is limited to startup registration of the fixed
// newlib device streams. It creates a FILE handle but performs no data
// transfer or readiness wait. Keep ordinary Fopen calls conservative: only
// this exact wrapper edge selects Fopen's executor-safe refinement.
//
//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func openBaremetalStandardStream(name, mode *Char) FilePtr {
	return Fopen(name, mode)
}

var Stdin FilePtr = openBaremetalStandardStream(Str("/dev/stdin"), Str("r"))
var Stdout FilePtr = openBaremetalStandardStream(Str("/dev/stdout"), Str("w"))
var Stderr FilePtr = Stdout
