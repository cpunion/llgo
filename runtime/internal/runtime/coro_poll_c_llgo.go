//go:build llgo && !baremetal

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

// The descriptor and poll leaf implementation belongs to the compiler runtime
// package that declares and consumes its scheduler-facing symbols. Keeping the
// C object here also makes those symbols available in programs which use the
// lightweight internal runtime without importing the standard-library runtime
// patch package.
const LLGoFiles = "../lib/runtime/_wrap/poll.c"
