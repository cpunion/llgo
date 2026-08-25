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

// Package abi contains the physical compiler/runtime contracts used by LLGo.
package abi

// PrintKindV1 identifies the physical payload stored in runtime.PrintArgV1.
//
// Keep this enum independent from target word size. Pointer-bearing payloads
// always use the descriptor's pointer fields; Word and Extra are reserved for
// scalar bits and target-sized lengths widened to uint64.
type PrintKindV1 uint8

const (
	PrintInvalidV1 PrintKindV1 = iota
	PrintBoolV1
	PrintIntV1
	PrintUintV1
	PrintFloatV1
	PrintComplexV1
	PrintPointerV1
	PrintStringV1
	PrintSliceV1
	PrintEfaceV1
	PrintIfaceV1
)

// PrintFlagNewlineV1 selects println spacing and its trailing newline.
const PrintFlagNewlineV1 uint8 = 1 << 0
