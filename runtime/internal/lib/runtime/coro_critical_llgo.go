//go:build llgo_coro

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

import _ "unsafe"

// coroCriticalEnter and coroCriticalExit preserve synchronous Go source while
// the compiler lowers their exact direct calls into one statically balanced
// preemption-mask region. They deliberately have no ordinary callable body and
// may not be converted to function or interface values.

//go:linkname coroCriticalEnter llgo.coroCriticalEnter
func coroCriticalEnter()

//go:linkname coroCriticalExit llgo.coroCriticalExit
func coroCriticalExit()
