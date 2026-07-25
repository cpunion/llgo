//go:build baremetal || wasm || tinygo.wasm

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

package runtime

import "unsafe"

// Host-pull WebAssembly and bare-metal profiles currently serialize runtime
// activation on one physical execution domain. Keep the legacy panic payload
// in that domain directly instead of importing a pthread TLS ABI which those
// targets do not provide.
type panicSlot struct {
	value unsafe.Pointer
}

func (*panicSlot) Create() {}

func (slot *panicSlot) Get() unsafe.Pointer {
	return slot.value
}

func (slot *panicSlot) Set(value unsafe.Pointer) {
	slot.value = value
}
