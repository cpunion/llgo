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

#ifndef LLGO_CORO_FLEET_OWNER_V1_H
#define LLGO_CORO_FLEET_OWNER_V1_H

#include <pthread.h>
#include <stdint.h>

uint32_t __llgo_coro_fleet_owner_count_v1(uint32_t maximum);
int __llgo_coro_fleet_factory_start_v1(void);
int __llgo_coro_fleet_owner_create_v3(
    pthread_t *thread, uint32_t *token, uint32_t slot);
int __llgo_coro_fleet_owner_try_reuse_v1(
    pthread_t *thread, uint32_t *token, uint32_t slot);
int __llgo_coro_fleet_owner_ready_v1(uint32_t slot);
int __llgo_coro_fleet_owner_join_v1(pthread_t thread, uint32_t token);
int __llgo_coro_fleet_owner_release_v1(
    pthread_t thread, uint32_t token, uint32_t slot);
int __llgo_coro_fleet_owner_retire_self_v1(uint32_t slot);
int __llgo_coro_fleet_owner_stop_standby_v1(uint32_t *joined);
int __llgo_coro_fleet_owner_yield_v1(void);
int __llgo_coro_fleet_factory_stop_v2(uint32_t terminal_owner_token);

#endif
