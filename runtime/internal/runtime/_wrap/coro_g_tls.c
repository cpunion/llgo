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

/*
 * The pthread key owns the physical executor thread and its destructor. This
 * separate C TLS word mirrors only the currently executing logical G, which
 * may change at every stackless coroutine resume. Keeping the leaf here avoids
 * a pthread_get/setspecific pair on each logical context switch.
 */
static _Thread_local void *llgo_coro_current_g_v1;

void *__llgo_coro_current_g_load_v1(void)
{
    return llgo_coro_current_g_v1;
}

void __llgo_coro_current_g_store_v1(void *g)
{
    llgo_coro_current_g_v1 = g;
}
