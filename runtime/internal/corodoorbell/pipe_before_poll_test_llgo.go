//go:build llgo && llgo_coro_native_ingress_test && (darwin || linux) && !baremetal

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

package corodoorbell

import _ "unsafe"

const nativeBeforePollHookEnabled = true

//go:linkname nativeIngressBeforePoll C.__llgo_coro_native_ingress_before_poll_v1
func nativeIngressBeforePoll() uint32

func nativeBeforePollHook() bool {
	return nativeIngressBeforePoll() == 1
}
