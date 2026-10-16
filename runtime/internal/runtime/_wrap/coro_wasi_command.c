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

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include <sys/types.h>
#include <unistd.h>
#include <wasi/api.h>

// wasi-libc's public path helpers accept NUL-terminated strings and silently
// compute strlen. Go already provides exact byte lengths, so import the raw
// Preview 1 functions rather than copying or reinterpreting those paths.
#define LLGO_WASI_IMPORT(name) __attribute__((import_module("wasi_snapshot_preview1"), import_name(#name)))
extern uint16_t llgo_wasi_path_open_v1(uint32_t, uint32_t, const char *, uint32_t, uint32_t, uint64_t, uint64_t, uint32_t, __wasi_fd_t *) LLGO_WASI_IMPORT(path_open);
extern uint16_t llgo_wasi_path_filestat_get_v1(uint32_t, uint32_t, const char *, uint32_t, __wasi_filestat_t *) LLGO_WASI_IMPORT(path_filestat_get);
extern uint16_t llgo_wasi_path_filestat_set_times_v1(uint32_t, uint32_t, const char *, uint32_t, uint64_t, uint64_t, uint32_t) LLGO_WASI_IMPORT(path_filestat_set_times);
extern uint16_t llgo_wasi_path_create_directory_v1(uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_create_directory);
extern uint16_t llgo_wasi_path_unlink_file_v1(uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_unlink_file);
extern uint16_t llgo_wasi_path_remove_directory_v1(uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_remove_directory);
extern uint16_t llgo_wasi_path_rename_v1(uint32_t, const char *, uint32_t, uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_rename);
extern uint16_t llgo_wasi_path_link_v1(uint32_t, uint32_t, const char *, uint32_t, uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_link);
extern uint16_t llgo_wasi_path_symlink_v1(const char *, uint32_t, uint32_t, const char *, uint32_t) LLGO_WASI_IMPORT(path_symlink);
extern uint16_t llgo_wasi_path_readlink_v1(uint32_t, const char *, uint32_t, char *, uint32_t, __wasi_size_t *) LLGO_WASI_IMPORT(path_readlink);
#undef LLGO_WASI_IMPORT

enum {
    llgo_host_action_none_v1 = 0,
    llgo_host_action_schedule_v1 = 1,
    llgo_host_action_alarm_v1 = 2,
    llgo_host_action_cancel_schedule_v1 = 3,
    llgo_host_action_cancel_alarm_v1 = 4,

    llgo_host_operation_none_v1 = 0,
    llgo_host_operation_submit_v1 = 1,
    llgo_host_operation_cancel_v1 = 2,

    llgo_host_callback_schedule_v1 = 1,
    llgo_host_callback_alarm_v1 = 2,

    llgo_drive_complete_v2 = 1,
    llgo_drive_suspended_v2 = 2,
    llgo_drive_yielded_v2 = 3,
    llgo_drive_repost_v2 = 6,

    llgo_run_more_v2 = 1u << 0,
    llgo_run_blocked_v2 = 1u << 1,
    llgo_run_has_deadline_v2 = 1u << 2,
    llgo_run_request_queued_v2 = 1u << 4,

    llgo_host_profile_wasi_v1 = 2,
    llgo_host_capability_schedule_v1 = 1u << 8,
    llgo_host_capability_alarm_v1 = 1u << 9,
    llgo_host_capability_reactor_poll_v1 = 1u << 10,
    llgo_host_capability_operation_v1 = 1u << 11,

    // internal/poll's deadline-aware operations retain the generic buffer ABI.
    llgo_host_file_read_v1 = (1u << 16) | 2u,
    llgo_host_file_write_v1 = (1u << 16) | 3u,
    llgo_host_wasi_fd_read_v1 = (3u << 16) | 1u,
    llgo_host_wasi_fd_write_v1 = (3u << 16) | 2u,
    llgo_host_wasi_fd_pread_v1 = (3u << 16) | 3u,
    llgo_host_wasi_fd_pwrite_v1 = (3u << 16) | 4u,
    llgo_host_wasi_fd_close_v1 = (3u << 16) | 5u,
    llgo_host_wasi_fd_seek_v1 = (3u << 16) | 6u,
    llgo_host_wasi_fd_readdir_v1 = (3u << 16) | 7u,
    llgo_host_wasi_fd_filestat_get_v1 = (3u << 16) | 8u,
    llgo_host_wasi_fd_filestat_set_size_v1 = (3u << 16) | 9u,
    llgo_host_wasi_fd_sync_v1 = (3u << 16) | 10u,
    llgo_host_wasi_fd_fdstat_set_rights_v1 = (3u << 16) | 11u,
    llgo_host_wasi_path_open_v1 = (3u << 16) | 12u,
    llgo_host_wasi_path_filestat_get_v1 = (3u << 16) | 13u,
    llgo_host_wasi_path_filestat_set_times_v1 = (3u << 16) | 14u,
    llgo_host_wasi_path_create_directory_v1 = (3u << 16) | 15u,
    llgo_host_wasi_path_unlink_file_v1 = (3u << 16) | 16u,
    llgo_host_wasi_path_remove_directory_v1 = (3u << 16) | 17u,
    llgo_host_wasi_path_rename_v1 = (3u << 16) | 18u,
    llgo_host_wasi_path_link_v1 = (3u << 16) | 19u,
    llgo_host_wasi_path_symlink_v1 = (3u << 16) | 20u,
    llgo_host_wasi_path_readlink_v1 = (3u << 16) | 21u,

    llgo_host_operation_capacity_v1 = 64,
    llgo_host_operation_max_args_v1 = 9,

    llgo_run_budget_v2 = 1024,
};

struct llgo_host_action_v1 {
    uint32_t kind;
    uint32_t executor_slot;
    uint32_t executor_generation;
    uint32_t epoch;
    uint32_t deadline_lo;
    uint32_t deadline_hi;
    uint32_t reserved0;
    uint32_t reserved1;
};

struct llgo_host_operation_v1 {
    uint32_t kind;
    uint32_t source_slot;
    uint32_t source_generation;
    uint32_t opcode;
    uint32_t arg_count;
    uint32_t reserved;
    uint32_t args[llgo_host_operation_max_args_v1 * 2];
};

struct llgo_run_result_v2 {
    uint32_t flags;
    uint32_t used;
    uint32_t executor_slot;
    uint32_t executor_generation;
    uint32_t epoch;
    uint32_t deadline_lo;
    uint32_t deadline_hi;
    uint32_t reserved;
};

_Static_assert(sizeof(struct llgo_host_action_v1) == 32,
               "invalid LLGo host action ABI");
_Static_assert(sizeof(struct llgo_host_operation_v1) == 96,
               "invalid LLGo host operation ABI");
_Static_assert(sizeof(struct llgo_run_result_v2) == 32,
               "invalid LLGo run result ABI");

extern uint32_t __llgo_coro_host_profile_v1(void);
extern uint32_t __llgo_coro_host_next_action_v1(
    struct llgo_host_action_v1 *);
extern uint32_t __llgo_coro_host_next_operation_v1(
    struct llgo_host_operation_v1 *);
extern bool __llgo_coro_host_publish_time_v1(uint32_t, uint32_t);
extern bool __llgo_coro_host_publish_wall_time_v1(
    uint32_t, uint32_t, uint32_t);
extern bool __llgo_coro_host_ack_cancel_v1(
    uint32_t, uint32_t, uint32_t, uint32_t);
extern uint32_t __llgo_coro_host_continue_slice_v1(
    uint32_t, uint32_t, uint32_t, uint32_t,
    uint32_t, uint32_t, uint32_t, struct llgo_run_result_v2 *);
extern uint32_t __llgo_coro_host_complete_operation_v1(
    uint32_t, uint32_t, uint32_t, uint32_t,
    uint32_t, uint32_t, uint32_t, uint32_t, uint32_t, uint32_t);

struct llgo_retained_action_v1 {
    bool active;
    bool ready;
    struct llgo_host_action_v1 value;
};

struct llgo_pending_operation_v1 {
    bool active;
    struct llgo_host_operation_v1 value;
};

static bool llgo_wasi_prepared_v1;
static bool llgo_wasi_reactor_entered_v1;
static struct llgo_retained_action_v1 llgo_wasi_schedule_v1;
static struct llgo_retained_action_v1 llgo_wasi_alarm_v1;
static struct llgo_pending_operation_v1
    llgo_wasi_operations_v1[llgo_host_operation_capacity_v1];
static __wasi_subscription_t
    llgo_wasi_subscriptions_v1[llgo_host_operation_capacity_v1 + 1];
static __wasi_event_t
    llgo_wasi_events_v1[llgo_host_operation_capacity_v1 + 1];

static uint64_t llgo_join_words_v1(uint32_t lo, uint32_t hi)
{
    return (uint64_t)lo | ((uint64_t)hi << 32);
}

static uint64_t llgo_operation_arg_v1(
    const struct llgo_host_operation_v1 *operation,
    uint32_t index)
{
    return llgo_join_words_v1(
        operation->args[index * 2],
        operation->args[index * 2 + 1]);
}

static bool llgo_all_zero_v1(const void *pointer, size_t size)
{
    const uint8_t *bytes = (const uint8_t *)pointer;
    for (size_t index = 0; index < size; index++) {
        if (bytes[index] != 0) {
            return false;
        }
    }
    return true;
}

static bool llgo_publish_clocks_v1(uint64_t *monotonic)
{
    __wasi_timestamp_t mono = 0;
    __wasi_timestamp_t wall = 0;
    if (__wasi_clock_time_get(
            __WASI_CLOCKID_MONOTONIC, 1, &mono) != __WASI_ERRNO_SUCCESS ||
        __wasi_clock_time_get(
            __WASI_CLOCKID_REALTIME, 1, &wall) != __WASI_ERRNO_SUCCESS ||
        mono > INT64_MAX) {
        return false;
    }
    uint64_t seconds = wall / UINT64_C(1000000000);
    uint32_t nanoseconds = (uint32_t)(wall % UINT64_C(1000000000));
    if (!__llgo_coro_host_publish_time_v1(
            (uint32_t)mono, (uint32_t)(mono >> 32)) ||
        !__llgo_coro_host_publish_wall_time_v1(
            (uint32_t)seconds, (uint32_t)(seconds >> 32), nanoseconds)) {
        return false;
    }
    if (monotonic != NULL) {
        *monotonic = mono;
    }
    return true;
}

uint32_t __llgo_coro_wasi_command_prepare_v1(void)
{
    const uint32_t required =
        llgo_host_capability_schedule_v1 |
        llgo_host_capability_alarm_v1 |
        llgo_host_capability_reactor_poll_v1 |
        llgo_host_capability_operation_v1;
    uint32_t profile = __llgo_coro_host_profile_v1();
    if (llgo_wasi_prepared_v1 || llgo_wasi_reactor_entered_v1 ||
        (profile & 0xffu) != llgo_host_profile_wasi_v1 ||
        (profile & required) != required ||
        !llgo_publish_clocks_v1(NULL)) {
        return 0;
    }
    llgo_wasi_prepared_v1 = true;
    return 1;
}

static bool llgo_same_action_v1(
    const struct llgo_host_action_v1 *left,
    const struct llgo_host_action_v1 *right)
{
    return left->executor_slot == right->executor_slot &&
           left->executor_generation == right->executor_generation &&
           left->epoch == right->epoch;
}

static bool llgo_take_action_v1(
    const struct llgo_host_action_v1 *action)
{
    if (action->reserved0 != 0 || action->reserved1 != 0 ||
        action->executor_slot == 0 ||
        action->executor_generation == 0 || action->epoch == 0) {
        return false;
    }
    struct llgo_retained_action_v1 *retained = NULL;
    switch (action->kind) {
    case llgo_host_action_schedule_v1:
        if (action->deadline_lo != 0 || action->deadline_hi != 0) {
            return false;
        }
        retained = &llgo_wasi_schedule_v1;
        break;
    case llgo_host_action_alarm_v1:
        retained = &llgo_wasi_alarm_v1;
        break;
    case llgo_host_action_cancel_schedule_v1:
        retained = &llgo_wasi_schedule_v1;
        break;
    case llgo_host_action_cancel_alarm_v1:
        retained = &llgo_wasi_alarm_v1;
        break;
    default:
        return false;
    }
    if (action->kind == llgo_host_action_cancel_schedule_v1 ||
        action->kind == llgo_host_action_cancel_alarm_v1) {
        if (!retained->active ||
            !llgo_same_action_v1(&retained->value, action) ||
            action->deadline_lo != 0 || action->deadline_hi != 0) {
            return false;
        }
        retained->active = false;
        retained->ready = false;
        memset(&retained->value, 0, sizeof(retained->value));
        return __llgo_coro_host_ack_cancel_v1(
            action->executor_slot,
            action->executor_generation,
            action->epoch,
            action->kind);
    }
    if (retained->active) {
        return false;
    }
    retained->active = true;
    retained->ready = false;
    retained->value = *action;
    return true;
}

static bool llgo_drain_actions_v1(void)
{
    for (;;) {
        struct llgo_host_action_v1 action;
        memset(&action, 0, sizeof(action));
        uint32_t kind = __llgo_coro_host_next_action_v1(&action);
        if (kind != action.kind) {
            return false;
        }
        if (kind == llgo_host_action_none_v1) {
            return llgo_all_zero_v1(&action, sizeof(action));
        }
        if (!llgo_take_action_v1(&action)) {
            return false;
        }
    }
}

static bool llgo_complete_operation_v1(
    const struct llgo_host_operation_v1 *operation,
    uint64_t r1,
    uint64_t r2,
    uint64_t error)
{
    return __llgo_coro_host_complete_operation_v1(
               operation->source_slot,
               operation->source_generation,
               0,
               3,
               (uint32_t)r1,
               (uint32_t)(r1 >> 32),
               (uint32_t)r2,
               (uint32_t)(r2 >> 32),
               (uint32_t)error,
               (uint32_t)(error >> 32)) == 1;
}

static bool llgo_fail_operation_v1(
    const struct llgo_host_operation_v1 *operation,
    int error)
{
    return llgo_complete_operation_v1(
        operation, UINT32_MAX, 0, (uint32_t)error);
}

static bool llgo_retire_descriptor_operations_v1(uint64_t descriptor)
{
    for (uint32_t index = 0; index < llgo_host_operation_capacity_v1; index++) {
        struct llgo_pending_operation_v1 *pending = &llgo_wasi_operations_v1[index];
        if (pending->active && llgo_operation_arg_v1(&pending->value, 0) == descriptor) {
            struct llgo_host_operation_v1 completed = pending->value;
            memset(pending, 0, sizeof(*pending));
            if (!llgo_fail_operation_v1(&completed, EBADF)) {
                return false;
            }
        }
    }
    return true;
}

// Class 3 preserves WASI's fd-relative ABI. Go owns preopen resolution and
// cwd; there is no second libc path resolver or fixed path-copy buffer.
static bool llgo_service_immediate_operation_v1(
    const struct llgo_host_operation_v1 *operation)
{
#define ARG(index) ((uint32_t)llgo_operation_arg_v1(operation, (index)))
#define PTR(type, index) ((type *)(uintptr_t)ARG(index))
#define WORD64(index) llgo_join_words_v1(ARG(index), ARG((index) + 1))
    __wasi_errno_t error;
    switch (operation->opcode) {
    case llgo_host_wasi_fd_close_v1:
        if (!llgo_retire_descriptor_operations_v1(ARG(0))) {
            return false;
        }
        error = __wasi_fd_close(ARG(0));
        break;
    case llgo_host_wasi_fd_seek_v1:
        if (ARG(3) > __WASI_WHENCE_END) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        error = __wasi_fd_seek(ARG(0), (int64_t)WORD64(1), ARG(3), PTR(__wasi_filesize_t, 4));
        break;
    case llgo_host_wasi_fd_readdir_v1:
        error = __wasi_fd_readdir(ARG(0), PTR(uint8_t, 1), ARG(2), WORD64(3), PTR(__wasi_size_t, 5));
        break;
    case llgo_host_wasi_fd_filestat_get_v1:
        error = __wasi_fd_filestat_get(ARG(0), PTR(__wasi_filestat_t, 1));
        break;
    case llgo_host_wasi_fd_filestat_set_size_v1:
        error = __wasi_fd_filestat_set_size(ARG(0), WORD64(1));
        break;
    case llgo_host_wasi_fd_sync_v1:
        error = __wasi_fd_sync(ARG(0));
        break;
    case llgo_host_wasi_fd_fdstat_set_rights_v1:
        error = __wasi_fd_fdstat_set_rights(ARG(0), WORD64(1), WORD64(3));
        break;
    case llgo_host_wasi_path_open_v1: {
        if (ARG(4) > UINT16_MAX || ARG(6) > UINT16_MAX) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        uint64_t memory_bytes = (uint64_t)__builtin_wasm_memory_size(0) << 16;
        if (ARG(5) == 0 || (uint64_t)ARG(5) + 16 > memory_bytes) {
            return llgo_fail_operation_v1(operation, EFAULT);
        }
        uint64_t capabilities[2];
        memcpy(capabilities, PTR(const void, 5), sizeof(capabilities));
        error = llgo_wasi_path_open_v1(ARG(0), ARG(1), PTR(const char, 2), ARG(3),
                                 ARG(4), capabilities[0], capabilities[1],
                                 ARG(6), PTR(__wasi_fd_t, 7));
        break;
    }
    case llgo_host_wasi_path_filestat_get_v1:
        error = llgo_wasi_path_filestat_get_v1(ARG(0), ARG(1), PTR(const char, 2), ARG(3), PTR(__wasi_filestat_t, 4));
        break;
    case llgo_host_wasi_path_filestat_set_times_v1:
        if (ARG(8) > UINT16_MAX) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        error = llgo_wasi_path_filestat_set_times_v1(ARG(0), ARG(1), PTR(const char, 2), ARG(3), WORD64(4), WORD64(6), ARG(8));
        break;
    case llgo_host_wasi_path_create_directory_v1:
        error = llgo_wasi_path_create_directory_v1(ARG(0), PTR(const char, 1), ARG(2));
        break;
    case llgo_host_wasi_path_unlink_file_v1:
        error = llgo_wasi_path_unlink_file_v1(ARG(0), PTR(const char, 1), ARG(2));
        break;
    case llgo_host_wasi_path_remove_directory_v1:
        error = llgo_wasi_path_remove_directory_v1(ARG(0), PTR(const char, 1), ARG(2));
        break;
    case llgo_host_wasi_path_rename_v1:
        error = llgo_wasi_path_rename_v1(ARG(0), PTR(const char, 1), ARG(2), ARG(3), PTR(const char, 4), ARG(5));
        break;
    case llgo_host_wasi_path_link_v1:
        error = llgo_wasi_path_link_v1(ARG(0), ARG(1), PTR(const char, 2), ARG(3), ARG(4), PTR(const char, 5), ARG(6));
        break;
    case llgo_host_wasi_path_symlink_v1:
        error = llgo_wasi_path_symlink_v1(PTR(const char, 0), ARG(1), ARG(2), PTR(const char, 3), ARG(4));
        break;
    case llgo_host_wasi_path_readlink_v1:
        error = llgo_wasi_path_readlink_v1(ARG(0), PTR(const char, 1), ARG(2), PTR(char, 3), ARG(4), PTR(__wasi_size_t, 5));
        break;
    default:
        return llgo_fail_operation_v1(operation, ENOSYS);
    }
    return llgo_complete_operation_v1(operation, 0, 0, error);
#undef WORD64
#undef PTR
#undef ARG
}

static struct llgo_pending_operation_v1 *llgo_find_operation_v1(
    uint32_t source_slot,
    uint32_t source_generation)
{
    for (uint32_t index = 0;
         index < llgo_host_operation_capacity_v1;
         index++) {
        struct llgo_pending_operation_v1 *pending =
            &llgo_wasi_operations_v1[index];
        if (pending->active &&
            pending->value.source_slot == source_slot &&
            pending->value.source_generation == source_generation) {
            return pending;
        }
    }
    return NULL;
}

static struct llgo_pending_operation_v1 *llgo_free_operation_v1(void)
{
    for (uint32_t index = 0;
         index < llgo_host_operation_capacity_v1;
         index++) {
        if (!llgo_wasi_operations_v1[index].active) {
            return &llgo_wasi_operations_v1[index];
        }
    }
    return NULL;
}

static bool llgo_file_operation_read_v1(uint32_t opcode)
{
    return opcode == llgo_host_file_read_v1 ||
           opcode == llgo_host_wasi_fd_read_v1 ||
           opcode == llgo_host_wasi_fd_pread_v1;
}

static bool llgo_file_buffer_operation_v1(uint32_t opcode)
{
    return opcode == llgo_host_file_read_v1 ||
           opcode == llgo_host_file_write_v1;
}

static bool llgo_take_operation_v1(
    const struct llgo_host_operation_v1 *operation)
{
    if (operation->reserved != 0 ||
        operation->source_slot == 0 ||
        operation->source_generation == 0 ||
        operation->opcode == 0 ||
        operation->arg_count > llgo_host_operation_max_args_v1) {
        return false;
    }
    if (operation->kind == llgo_host_operation_cancel_v1) {
        struct llgo_pending_operation_v1 *pending =
            llgo_find_operation_v1(
                operation->source_slot,
                operation->source_generation);
        if (pending == NULL ||
            pending->value.opcode != operation->opcode ||
            pending->value.arg_count != operation->arg_count ||
            memcmp(
                pending->value.args,
                operation->args,
                sizeof(operation->args)) != 0) {
            return false;
        }
        struct llgo_host_operation_v1 canceled = pending->value;
        memset(pending, 0, sizeof(*pending));
        return llgo_fail_operation_v1(&canceled, ECANCELED);
    }
    if (operation->kind != llgo_host_operation_submit_v1 ||
        llgo_find_operation_v1(
            operation->source_slot,
            operation->source_generation) != NULL) {
        return false;
    }
    // Exact class/arity is checked before any borrowed pointer is interpreted.
    static const uint8_t argument_counts[] = { 4, 4, 6, 6, 1, 5, 6, 2, 3, 1, 5, 8, 5, 9, 3, 3, 3, 6, 7, 5, 6 };
    uint32_t verb = operation->opcode - (3u << 16) - 1u;
    bool buffer_operation = llgo_file_buffer_operation_v1(operation->opcode);
    if (!buffer_operation && verb >= sizeof(argument_counts)) {
        return llgo_fail_operation_v1(operation, ENOSYS);
    }
    uint32_t arg_count = buffer_operation ? 3 : argument_counts[verb];
    if (operation->arg_count != arg_count) {
        return llgo_fail_operation_v1(operation, EINVAL);
    }
    for (uint32_t index = 0; index < operation->arg_count; index++) {
        if (llgo_operation_arg_v1(operation, index) > UINT32_MAX) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
    }
    if (!buffer_operation && verb >= 4) {
        return llgo_service_immediate_operation_v1(operation);
    }
    struct llgo_pending_operation_v1 *pending = llgo_free_operation_v1();
    if (pending == NULL) {
        return false;
    }
    pending->value = *operation;
    pending->active = true;
    return true;
}

static bool llgo_drain_operations_v1(void)
{
    for (;;) {
        struct llgo_host_operation_v1 operation;
        memset(&operation, 0, sizeof(operation));
        uint32_t kind = __llgo_coro_host_next_operation_v1(&operation);
        if (kind != operation.kind) {
            return false;
        }
        if (kind == llgo_host_operation_none_v1) {
            return llgo_all_zero_v1(&operation, sizeof(operation));
        }
        if (!llgo_take_operation_v1(&operation)) {
            return false;
        }
    }
}

static bool llgo_service_ready_operation_v1(
    struct llgo_pending_operation_v1 *pending)
{
    const struct llgo_host_operation_v1 *operation = &pending->value;
    __wasi_fd_t descriptor = (uint32_t)llgo_operation_arg_v1(operation, 0);
    uintptr_t vectors = (uintptr_t)llgo_operation_arg_v1(operation, 1);
    __wasi_size_t count = (uint32_t)llgo_operation_arg_v1(operation, 2);
    bool buffer_operation = llgo_file_buffer_operation_v1(operation->opcode);
    __wasi_iovec_t vector;
    __wasi_size_t transferred = 0;
    if (buffer_operation) {
        // The host call is synchronous; this adapter has no retained native
        // frame. The managed source keeps the original buffer rooted until
        // exact completion/cancellation, just as for raw WASI iovecs.
        vector.buf = (void *)vectors;
        vector.buf_len = count;
        vectors = (uintptr_t)&vector;
        count = 1;
    }
    __wasi_errno_t error;
    if (operation->opcode == llgo_host_wasi_fd_read_v1 ||
        operation->opcode == llgo_host_file_read_v1) {
        __wasi_size_t *result = buffer_operation ? &transferred :
            (__wasi_size_t *)(uintptr_t)llgo_operation_arg_v1(operation, 3);
        error = __wasi_fd_read(descriptor, (const __wasi_iovec_t *)vectors, count, result);
    } else if (operation->opcode == llgo_host_wasi_fd_write_v1 ||
               operation->opcode == llgo_host_file_write_v1) {
        __wasi_size_t *result = buffer_operation ? &transferred :
            (__wasi_size_t *)(uintptr_t)llgo_operation_arg_v1(operation, 3);
        error = __wasi_fd_write(descriptor, (const __wasi_ciovec_t *)vectors, count, result);
    } else if (operation->opcode == llgo_host_wasi_fd_pread_v1 ||
               operation->opcode == llgo_host_wasi_fd_pwrite_v1) {
        uint64_t offset = llgo_operation_arg_v1(operation, 3) |
                         (llgo_operation_arg_v1(operation, 4) << 32);
        __wasi_size_t *result = (__wasi_size_t *)(uintptr_t)llgo_operation_arg_v1(operation, 5);
        // Preview 1 imports are synchronous even for regular-file metadata
        // and I/O. Polling cannot promise nonblocking disk/host execution.
        if (llgo_file_operation_read_v1(operation->opcode)) {
            error = __wasi_fd_pread(descriptor, (const __wasi_iovec_t *)vectors, count, offset, result);
        } else {
            error = __wasi_fd_pwrite(descriptor, (const __wasi_ciovec_t *)vectors, count, offset, result);
        }
    } else {
        return false;
    }
    if (error == __WASI_ERRNO_AGAIN || error == __WASI_ERRNO_INTR) {
        return true;
    }
    struct llgo_host_operation_v1 completed = pending->value;
    memset(pending, 0, sizeof(*pending));
    return llgo_complete_operation_v1(&completed,
        buffer_operation ? (error ? UINT32_MAX : transferred) : 0, 0, error);
}

static bool llgo_validate_run_result_v1(
    uint32_t status,
    const struct llgo_run_result_v2 *result)
{
    if (result->reserved != 0 || result->used > llgo_run_budget_v2) {
        return false;
    }
    if (status == llgo_drive_complete_v2) {
        return result->flags == 0 &&
               result->executor_slot == 0 &&
               result->executor_generation == 0 &&
               result->epoch == 0 &&
               result->deadline_lo == 0 &&
               result->deadline_hi == 0;
    }
    if (status == llgo_drive_yielded_v2 ||
        status == llgo_drive_repost_v2) {
        return result->flags ==
                   (llgo_run_more_v2 | llgo_run_request_queued_v2) &&
               result->executor_slot != 0 &&
               result->executor_generation != 0 &&
               result->epoch != 0 &&
               result->deadline_lo == 0 &&
               result->deadline_hi == 0;
    }
    if (status == llgo_drive_suspended_v2) {
        bool has_deadline =
            (result->flags & llgo_run_has_deadline_v2) != 0;
        return (result->flags &
                    ~(llgo_run_blocked_v2 | llgo_run_has_deadline_v2)) == 0 &&
               (result->flags & llgo_run_blocked_v2) != 0 &&
               result->executor_slot != 0 &&
               result->executor_generation != 0 &&
               result->epoch != 0 &&
               (has_deadline ||
                (result->deadline_lo == 0 &&
                 result->deadline_hi == 0));
    }
    return false;
}

static uint32_t llgo_continue_action_v1(
    struct llgo_retained_action_v1 *retained,
    uint32_t cause)
{
    struct llgo_host_action_v1 action = retained->value;
    retained->active = false;
    retained->ready = false;
    memset(&retained->value, 0, sizeof(retained->value));
    uint64_t now = 0;
    if (!llgo_publish_clocks_v1(&now)) {
        return 0;
    }
    struct llgo_run_result_v2 result;
    memset(&result, 0, sizeof(result));
    uint32_t status = __llgo_coro_host_continue_slice_v1(
        action.executor_slot,
        action.executor_generation,
        action.epoch,
        cause,
        (uint32_t)now,
        (uint32_t)(now >> 32),
        llgo_run_budget_v2,
        &result);
    if (!llgo_validate_run_result_v1(status, &result)) {
        return 0;
    }
    return status;
}

static __wasi_errno_t llgo_make_nonblocking_v1(int descriptor)
{
    __wasi_fdstat_t state;
    __wasi_errno_t error = __wasi_fd_fdstat_get(descriptor, &state);
    if (error != __WASI_ERRNO_SUCCESS) {
        return error;
    }
    if ((state.fs_flags & __WASI_FDFLAGS_NONBLOCK) != 0) {
        return __WASI_ERRNO_SUCCESS;
    }
    if ((state.fs_rights_base & __WASI_RIGHTS_FD_FDSTAT_SET_FLAGS) == 0) {
        // Inherited host streams can expose only FD_READ/FD_WRITE, with no
        // mutable fd flags. Do not attempt a forbidden metadata operation:
        // poll_oneoff still decides whether this host can service the stream.
        // As with regular files, Preview 1 cannot guarantee a nonblocking
        // synchronous host import after readiness has been reported.
        return __WASI_ERRNO_SUCCESS;
    }
    error = __wasi_fd_fdstat_set_flags(
        descriptor,
        state.fs_flags | __WASI_FDFLAGS_NONBLOCK);
    // Some regular-file hosts report NOTSUP. Readiness and positional imports
    // still inherit Preview 1's synchronous-host I/O limitation.
    return error == __WASI_ERRNO_NOTSUP
               ? __WASI_ERRNO_SUCCESS
               : error;
}

static bool llgo_build_poll_set_v1(uint32_t *count)
{
    *count = 0;
    memset(
        llgo_wasi_subscriptions_v1,
        0,
        sizeof(llgo_wasi_subscriptions_v1));
    for (uint32_t index = 0;
         index < llgo_host_operation_capacity_v1;
         index++) {
        struct llgo_pending_operation_v1 *pending =
            &llgo_wasi_operations_v1[index];
        if (!pending->active) {
            continue;
        }
        int descriptor =
            (int)llgo_operation_arg_v1(&pending->value, 0);
        __wasi_errno_t error = llgo_make_nonblocking_v1(descriptor);
        if (error != __WASI_ERRNO_SUCCESS) {
            struct llgo_host_operation_v1 failed = pending->value;
            memset(pending, 0, sizeof(*pending));
            if (!llgo_fail_operation_v1(&failed, error)) {
                return false;
            }
            continue;
        }
        if (pending->value.opcode == llgo_host_wasi_fd_pread_v1 ||
            pending->value.opcode == llgo_host_wasi_fd_pwrite_v1) {
            // A pipe/socket must report ESPIPE even when it has no readable
            // data. Try the nonblocking positional operation before waiting;
            // only EAGAIN/EINTR keep the same cancelable pending obligation.
            if (!llgo_service_ready_operation_v1(pending)) {
                return false;
            }
            if (!pending->active) {
                continue;
            }
        }
        __wasi_subscription_t *subscription =
            &llgo_wasi_subscriptions_v1[*count];
        subscription->userdata = (uint64_t)index + 1;
        if (llgo_file_operation_read_v1(pending->value.opcode)) {
            subscription->u.tag = __WASI_EVENTTYPE_FD_READ;
            subscription->u.u.fd_read.file_descriptor = descriptor;
        } else if (pending->value.opcode == llgo_host_file_write_v1 ||
                   pending->value.opcode == llgo_host_wasi_fd_write_v1 ||
                   pending->value.opcode == llgo_host_wasi_fd_pwrite_v1) {
            subscription->u.tag = __WASI_EVENTTYPE_FD_WRITE;
            subscription->u.u.fd_write.file_descriptor = descriptor;
        } else {
            return false;
        }
        (*count)++;
    }
    if (llgo_wasi_alarm_v1.active && !llgo_wasi_alarm_v1.ready) {
        __wasi_subscription_t *subscription =
            &llgo_wasi_subscriptions_v1[*count];
        subscription->userdata = 0;
        subscription->u.tag = __WASI_EVENTTYPE_CLOCK;
        subscription->u.u.clock.id = __WASI_CLOCKID_MONOTONIC;
        subscription->u.u.clock.timeout = llgo_join_words_v1(
            llgo_wasi_alarm_v1.value.deadline_lo,
            llgo_wasi_alarm_v1.value.deadline_hi);
        subscription->u.u.clock.precision = 1;
        subscription->u.u.clock.flags =
            __WASI_SUBCLOCKFLAGS_SUBSCRIPTION_CLOCK_ABSTIME;
        (*count)++;
    }
    return true;
}

static bool llgo_service_poll_events_v1(uint32_t count)
{
    for (uint32_t index = 0; index < count; index++) {
        const __wasi_event_t *event = &llgo_wasi_events_v1[index];
        if (event->userdata == 0) {
            if (!llgo_wasi_alarm_v1.active ||
                event->type != __WASI_EVENTTYPE_CLOCK ||
                event->error != __WASI_ERRNO_SUCCESS) {
                return false;
            }
            llgo_wasi_alarm_v1.ready = true;
            continue;
        }
        uint64_t pending_index = event->userdata - 1;
        if (pending_index >= llgo_host_operation_capacity_v1) {
            return false;
        }
        struct llgo_pending_operation_v1 *pending =
            &llgo_wasi_operations_v1[pending_index];
        if (!pending->active) {
            return false;
        }
        uint8_t expected =
            llgo_file_operation_read_v1(pending->value.opcode)
                ? __WASI_EVENTTYPE_FD_READ
                : __WASI_EVENTTYPE_FD_WRITE;
        if (event->type != expected) {
            return false;
        }
        if (event->error != __WASI_ERRNO_SUCCESS) {
            struct llgo_host_operation_v1 failed = pending->value;
            memset(pending, 0, sizeof(*pending));
            if (!llgo_fail_operation_v1(&failed, event->error)) {
                return false;
            }
            continue;
        }
        if (!llgo_service_ready_operation_v1(pending)) {
            return false;
        }
    }
    return true;
}

static bool llgo_service_unpollable_files_v1(void)
{
    bool found = false;
    for (uint32_t index = 0;
         index < llgo_host_operation_capacity_v1;
         index++) {
        struct llgo_pending_operation_v1 *pending =
            &llgo_wasi_operations_v1[index];
        if (!pending->active) {
            continue;
        }
        found = true;
        __wasi_fdstat_t state;
        __wasi_errno_t error = __wasi_fd_fdstat_get(
            (uint32_t)llgo_operation_arg_v1(&pending->value, 0), &state);
        if (error == __WASI_ERRNO_SUCCESS &&
            state.fs_filetype != __WASI_FILETYPE_REGULAR_FILE &&
            (state.fs_flags & __WASI_FDFLAGS_NONBLOCK) == 0) {
            // A stream without mutable NONBLOCK flags must have readiness
            // support. NOTSUP is not permission to perform a blocking read.
            error = __WASI_ERRNO_NOTSUP;
        }
        if (error != __WASI_ERRNO_SUCCESS) {
            struct llgo_host_operation_v1 failed = pending->value;
            memset(pending, 0, sizeof(*pending));
            if (!llgo_fail_operation_v1(&failed, error)) {
                return false;
            }
            continue;
        }
        if (!llgo_service_ready_operation_v1(pending)) {
            return false;
        }
        if (pending->active) {
            struct llgo_host_operation_v1 failed = pending->value;
            memset(pending, 0, sizeof(*pending));
            if (!llgo_fail_operation_v1(&failed, ENOTSUP)) {
                return false;
            }
        }
    }
    return found;
}

uint32_t __llgo_coro_wasi_command_reactor_v1(void)
{
    if (!llgo_wasi_prepared_v1 || llgo_wasi_reactor_entered_v1) {
        return 0;
    }
    llgo_wasi_reactor_entered_v1 = true;
    for (;;) {
        if (!llgo_drain_operations_v1() ||
            !llgo_drain_actions_v1()) {
            return 0;
        }
        if (llgo_wasi_schedule_v1.active) {
            uint32_t status = llgo_continue_action_v1(
                &llgo_wasi_schedule_v1,
                llgo_host_callback_schedule_v1);
            if (status == llgo_drive_complete_v2) {
                return 1;
            }
            if (status == 0) {
                return 0;
            }
            continue;
        }
        if (llgo_wasi_alarm_v1.active &&
            llgo_wasi_alarm_v1.ready) {
            uint32_t status = llgo_continue_action_v1(
                &llgo_wasi_alarm_v1,
                llgo_host_callback_alarm_v1);
            if (status == llgo_drive_complete_v2) {
                return 1;
            }
            if (status == 0) {
                return 0;
            }
            continue;
        }

        uint32_t subscription_count = 0;
        if (!llgo_build_poll_set_v1(&subscription_count)) {
            return 0;
        }
        // Descriptor errors or an immediate positional completion may have
        // posted runnable work while building the set. Consume its exact
        // schedule/cancel actions before sleeping on any remaining deadline.
        if (!llgo_drain_actions_v1()) {
            return 0;
        }
        if (llgo_wasi_schedule_v1.active ||
            (llgo_wasi_alarm_v1.active && llgo_wasi_alarm_v1.ready)) {
            continue;
        }
        if (subscription_count == 0) {
            return 0;
        }
        memset(llgo_wasi_events_v1, 0, sizeof(llgo_wasi_events_v1));
        __wasi_size_t event_count = 0;
        __wasi_errno_t error = __wasi_poll_oneoff(
            llgo_wasi_subscriptions_v1,
            llgo_wasi_events_v1,
            subscription_count,
            &event_count);
        if (error == __WASI_ERRNO_INTR) {
            continue;
        }
        if (error == __WASI_ERRNO_NOTSUP) {
            if (!llgo_service_unpollable_files_v1()) {
                return 0;
            }
            continue;
        }
        if (error != __WASI_ERRNO_SUCCESS ||
            event_count == 0 ||
            event_count > subscription_count ||
            !llgo_publish_clocks_v1(NULL) ||
            !llgo_service_poll_events_v1(event_count)) {
            return 0;
        }
        // Re-enter the outer loop before firing a simultaneous alarm. An I/O
        // completion may have requested Schedule and exact Alarm cancellation;
        // draining those actions first prevents a stale timer callback from
        // racing the operation result or touching a canceled borrowed buffer.
    }
}
