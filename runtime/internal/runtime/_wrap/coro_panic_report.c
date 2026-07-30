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

#include <stdio.h>

/*
 * The logical-panic reporter runs only after the command scheduler has
 * returned and released ownership. Keeping this one physical leaf distinct
 * from ordinary fputc lets the compiler prove that all of its Go occurrences
 * belong to the raw no-return reporter without assigning fputc a global
 * non-blocking or synchronous contract.
 */
int __llgo_coro_panic_fputc_v1(int ch, FILE *stream)
{
    return fputc(ch, stream);
}
