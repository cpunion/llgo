//go:build baremetal && !wasm && !nogc

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package tinygogc

// Preserve the bare-metal collector's capacity-triggered policy.
func gcAutomaticAllowed() bool         { return true }
func gcAllocationDue(size uint64) bool { return false }
func gcCollectionComplete()            {}
func gcNextGoal() uint64               { return 0 }
