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

    llgo_host_file_open_v1 = (1u << 16) | 1u,
    llgo_host_file_read_v1 = (1u << 16) | 2u,
    llgo_host_file_write_v1 = (1u << 16) | 3u,
    llgo_host_file_close_v1 = (1u << 16) | 4u,
    llgo_host_file_seek_v1 = (1u << 16) | 5u,
    llgo_host_file_unlink_v1 = (1u << 16) | 6u,

    llgo_host_operation_max_args_v1 = 9,
    llgo_host_operation_capacity_v1 = 64,
    llgo_wasi_path_capacity_v1 = 4096,
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
static char llgo_wasi_path_v1[llgo_wasi_path_capacity_v1 + 1];
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

static bool llgo_copy_path_v1(
    const struct llgo_host_operation_v1 *operation,
    uint32_t pointer_index,
    uint32_t length_index)
{
    uint64_t raw_pointer =
        llgo_operation_arg_v1(operation, pointer_index);
    uint64_t length = llgo_operation_arg_v1(operation, length_index);
    if (raw_pointer > UINTPTR_MAX ||
        length > llgo_wasi_path_capacity_v1 ||
        (length != 0 && raw_pointer == 0)) {
        return false;
    }
    uintptr_t pointer = (uintptr_t)raw_pointer;
    if (length != 0) {
        memcpy(llgo_wasi_path_v1, (const void *)pointer, (size_t)length);
    }
    llgo_wasi_path_v1[length] = '\0';
    return true;
}

enum {
    llgo_go_o_wronly_v1 = 1,
    llgo_go_o_rdwr_v1 = 2,
    llgo_go_o_create_v1 = 0100,
    llgo_go_o_excl_v1 = 0200,
    llgo_go_o_nofollow_v1 = 0400,
    llgo_go_o_trunc_v1 = 01000,
    llgo_go_o_append_v1 = 02000,
    llgo_go_o_sync_v1 = 010000,
    llgo_go_o_directory_v1 = 020000,
};

static bool llgo_translate_open_flags_v1(uint32_t go_flags, int *flags)
{
    const uint32_t known =
        3u |
        llgo_go_o_create_v1 |
        llgo_go_o_excl_v1 |
        llgo_go_o_nofollow_v1 |
        llgo_go_o_trunc_v1 |
        llgo_go_o_append_v1 |
        llgo_go_o_sync_v1 |
        llgo_go_o_directory_v1;
    if (flags == NULL || (go_flags & ~known) != 0) {
        return false;
    }
    switch (go_flags & 3u) {
    case 0:
        *flags = O_RDONLY;
        break;
    case llgo_go_o_wronly_v1:
        *flags = O_WRONLY;
        break;
    case llgo_go_o_rdwr_v1:
        *flags = O_RDWR;
        break;
    default:
        return false;
    }
    if ((go_flags & llgo_go_o_create_v1) != 0) {
        *flags |= O_CREAT;
    }
    if ((go_flags & llgo_go_o_excl_v1) != 0) {
        *flags |= O_EXCL;
    }
    if ((go_flags & llgo_go_o_nofollow_v1) != 0) {
        *flags |= O_NOFOLLOW;
    }
    if ((go_flags & llgo_go_o_trunc_v1) != 0) {
        *flags |= O_TRUNC;
    }
    if ((go_flags & llgo_go_o_append_v1) != 0) {
        *flags |= O_APPEND;
    }
    if ((go_flags & llgo_go_o_sync_v1) != 0) {
        *flags |= O_SYNC;
    }
    if ((go_flags & llgo_go_o_directory_v1) != 0) {
        *flags |= O_DIRECTORY;
    }
    // The synchronous Go wrapper owns blocking semantics. The reactor keeps
    // the underlying descriptor nonblocking and retries EAGAIN after
    // poll_oneoff, so no individual read/write import can retain the command.
    *flags |= O_NONBLOCK;
    return true;
}

static bool llgo_service_immediate_operation_v1(
    const struct llgo_host_operation_v1 *operation)
{
    uint64_t r1 = 0;
    uint64_t r2 = 0;
    switch (operation->opcode) {
    case llgo_host_file_open_v1: {
        if (operation->arg_count != 4) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        if (!llgo_copy_path_v1(operation, 0, 1)) {
            return llgo_fail_operation_v1(operation, ENAMETOOLONG);
        }
        uint64_t go_flags = llgo_operation_arg_v1(operation, 2);
        uint64_t permissions = llgo_operation_arg_v1(operation, 3);
        if (go_flags > UINT32_MAX || permissions > UINT32_MAX) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        int flags = 0;
        if (!llgo_translate_open_flags_v1(
                (uint32_t)go_flags, &flags)) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        errno = 0;
        int descriptor = open(
            llgo_wasi_path_v1,
            flags,
            (mode_t)permissions);
        if (descriptor < 0) {
            return llgo_fail_operation_v1(operation, errno);
        }
        r1 = (uint32_t)descriptor;
        break;
    }
    case llgo_host_file_close_v1:
        if (operation->arg_count != 1) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        if (llgo_operation_arg_v1(operation, 0) > INT_MAX) {
            return llgo_fail_operation_v1(operation, EBADF);
        }
        errno = 0;
        if (close((int)llgo_operation_arg_v1(operation, 0)) != 0) {
            return llgo_fail_operation_v1(operation, errno);
        }
        break;
    case llgo_host_file_seek_v1: {
        if (operation->arg_count != 4) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        if (llgo_operation_arg_v1(operation, 0) > INT_MAX ||
            llgo_operation_arg_v1(operation, 1) > UINT32_MAX ||
            llgo_operation_arg_v1(operation, 2) > UINT32_MAX ||
            llgo_operation_arg_v1(operation, 3) > INT_MAX) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        uint64_t offset =
            (uint64_t)(uint32_t)llgo_operation_arg_v1(operation, 1) |
            ((uint64_t)(uint32_t)llgo_operation_arg_v1(operation, 2) << 32);
        errno = 0;
        off_t result = lseek(
            (int)llgo_operation_arg_v1(operation, 0),
            (off_t)(int64_t)offset,
            (int)llgo_operation_arg_v1(operation, 3));
        if (result == (off_t)-1) {
            return llgo_fail_operation_v1(operation, errno);
        }
        uint64_t word = (uint64_t)(int64_t)result;
        r1 = (uint32_t)word;
        r2 = (uint32_t)(word >> 32);
        break;
    }
    case llgo_host_file_unlink_v1:
        if (operation->arg_count != 2) {
            return llgo_fail_operation_v1(operation, EINVAL);
        }
        if (!llgo_copy_path_v1(operation, 0, 1)) {
            return llgo_fail_operation_v1(operation, ENAMETOOLONG);
        }
        errno = 0;
        if (unlink(llgo_wasi_path_v1) != 0) {
            return llgo_fail_operation_v1(operation, errno);
        }
        break;
    default:
        // Preview 1 has no standard socket creation/connect surface. Every
        // unsupported HostOp is completed explicitly instead of being dropped
        // or accidentally executed on the managed scheduler stack.
        return llgo_fail_operation_v1(operation, ENOSYS);
    }
    return llgo_complete_operation_v1(operation, r1, r2, 0);
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
    if (operation->opcode != llgo_host_file_read_v1 &&
        operation->opcode != llgo_host_file_write_v1) {
        return llgo_service_immediate_operation_v1(operation);
    }
    if (operation->arg_count != 3) {
        return llgo_fail_operation_v1(operation, EINVAL);
    }
    uint64_t descriptor = llgo_operation_arg_v1(operation, 0);
    uint64_t pointer = llgo_operation_arg_v1(operation, 1);
    uint64_t length = llgo_operation_arg_v1(operation, 2);
    if (descriptor > INT_MAX) {
        return llgo_fail_operation_v1(operation, EBADF);
    }
    if (pointer > UINTPTR_MAX || length > SIZE_MAX ||
        (length != 0 && pointer == 0)) {
        return llgo_fail_operation_v1(operation, EFAULT);
    }
    if (length == 0) {
        return llgo_complete_operation_v1(operation, 0, 0, 0);
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
    int descriptor = (int)llgo_operation_arg_v1(operation, 0);
    void *buffer = (void *)(uintptr_t)llgo_operation_arg_v1(operation, 1);
    size_t length = (size_t)llgo_operation_arg_v1(operation, 2);
    errno = 0;
    ssize_t result;
    if (operation->opcode == llgo_host_file_read_v1) {
        result = read(descriptor, buffer, length);
    } else if (operation->opcode == llgo_host_file_write_v1) {
        result = write(descriptor, buffer, length);
    } else {
        return false;
    }
    if (result < 0 && (errno == EAGAIN || errno == EINTR)) {
        return true;
    }
    struct llgo_host_operation_v1 completed = pending->value;
    memset(pending, 0, sizeof(*pending));
    if (result < 0) {
        return llgo_fail_operation_v1(&completed, errno);
    }
    return llgo_complete_operation_v1(
        &completed, (uint64_t)(size_t)result, 0, 0);
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
    error = __wasi_fd_fdstat_set_flags(
        descriptor,
        state.fs_flags | __WASI_FDFLAGS_NONBLOCK);
    // Some regular-file hosts report NOTSUP even though poll_oneoff always
    // reports the descriptor ready. The subsequent operation is still guarded
    // by that readiness event.
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
        __wasi_subscription_t *subscription =
            &llgo_wasi_subscriptions_v1[*count];
        subscription->userdata = (uint64_t)index + 1;
        if (pending->value.opcode == llgo_host_file_read_v1) {
            subscription->u.tag = __WASI_EVENTTYPE_FD_READ;
            subscription->u.u.fd_read.file_descriptor = descriptor;
        } else if (pending->value.opcode == llgo_host_file_write_v1) {
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
            pending->value.opcode == llgo_host_file_read_v1
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
