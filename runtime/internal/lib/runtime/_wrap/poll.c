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

#include <stdint.h>

#if defined(__APPLE__) || defined(__linux__)

#include <errno.h>
#include <poll.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <unistd.h>

/*
 * Opaque scalar-only state behind internal/poll's uintptr context ABI.
 *
 * Keeping the allocation and all address reconstruction in C means an LLVM
 * coroutine frame never carries an untraceable Go pointer. Descriptors are
 * independent, so concurrent opens/closes need no shared catalog lock and
 * have no fixed table capacity. internal/poll frees a context only after its
 * FD reference count reaches zero.
 */
struct llgo_runtime_poll_desc_v1 {
    int32_t fd;
    _Atomic uint32_t closing;
    _Atomic int64_t read_deadline;
    _Atomic int64_t write_deadline;
    _Atomic uint64_t read_operation;
    _Atomic uint64_t write_operation;
    uint32_t inline_stream;
};

#define LLGO_RUNTIME_POLL_DESC_CLOSING_V1 (UINT64_C(1) << 32)
#define LLGO_RUNTIME_POLL_DESC_INLINE_STREAM_V1 (UINT64_C(1) << 33)

static uint64_t llgo_runtime_poll_desc_pack_state_v1(
    const struct llgo_runtime_poll_desc_v1 *desc,
    uint32_t closing)
{
    uint64_t state = (uint64_t)(uint32_t)desc->fd;
    if (closing != 0) {
        state |= LLGO_RUNTIME_POLL_DESC_CLOSING_V1;
    }
    if (desc->inline_stream != 0) {
        state |= LLGO_RUNTIME_POLL_DESC_INLINE_STREAM_V1;
    }
    return state;
}

uintptr_t __llgo_runtime_poll_desc_alloc_v1(
    int32_t fd,
    uint32_t inline_stream)
{
    int saved_errno = errno;
    struct llgo_runtime_poll_desc_v1 *desc =
        calloc(1, sizeof(struct llgo_runtime_poll_desc_v1));
    if (desc != NULL) {
        desc->fd = fd;
        atomic_init(&desc->closing, 0);
        atomic_init(&desc->read_deadline, 0);
        atomic_init(&desc->write_deadline, 0);
        atomic_init(&desc->read_operation, 0);
        atomic_init(&desc->write_operation, 0);
        desc->inline_stream = inline_stream != 0;
    }
    errno = saved_errno;
    return (uintptr_t)desc;
}

void __llgo_runtime_poll_desc_free_v1(uintptr_t context)
{
    int saved_errno = errno;
    free((void *)context);
    errno = saved_errno;
}

uint64_t __llgo_runtime_poll_desc_state_v1(uintptr_t context)
{
    const struct llgo_runtime_poll_desc_v1 *desc =
        (const struct llgo_runtime_poll_desc_v1 *)context;
    if (desc == NULL) {
        return LLGO_RUNTIME_POLL_DESC_CLOSING_V1;
    }
    return llgo_runtime_poll_desc_pack_state_v1(
        desc,
        atomic_load_explicit(&desc->closing, memory_order_acquire));
}

int64_t __llgo_runtime_poll_desc_deadline_v1(
    uintptr_t context,
    int32_t mode)
{
    const struct llgo_runtime_poll_desc_v1 *desc =
        (const struct llgo_runtime_poll_desc_v1 *)context;
    if (desc == NULL) {
        return 0;
    }
    if (mode == 'r') {
        return atomic_load_explicit(&desc->read_deadline, memory_order_seq_cst);
    }
    if (mode == 'w') {
        return atomic_load_explicit(&desc->write_deadline, memory_order_seq_cst);
    }
    int64_t read_deadline =
        atomic_load_explicit(&desc->read_deadline, memory_order_seq_cst);
    int64_t write_deadline =
        atomic_load_explicit(&desc->write_deadline, memory_order_seq_cst);
    if (read_deadline == 0) {
        return write_deadline;
    }
    if (write_deadline == 0 || read_deadline < write_deadline) {
        return read_deadline;
    }
    return write_deadline;
}

void __llgo_runtime_poll_desc_set_deadline_v1(
    uintptr_t context,
    int32_t mode,
    int64_t deadline)
{
    struct llgo_runtime_poll_desc_v1 *desc =
        (struct llgo_runtime_poll_desc_v1 *)context;
    if (desc == NULL) {
        return;
    }
    if (mode == 'r' || mode == 'r' + 'w') {
        atomic_store_explicit(
            &desc->read_deadline, deadline, memory_order_seq_cst);
    }
    if (mode == 'w' || mode == 'r' + 'w') {
        atomic_store_explicit(
            &desc->write_deadline, deadline, memory_order_seq_cst);
    }
}

uint64_t __llgo_runtime_poll_desc_mark_closing_v1(uintptr_t context)
{
    struct llgo_runtime_poll_desc_v1 *desc =
        (struct llgo_runtime_poll_desc_v1 *)context;
    if (desc == NULL) {
        return LLGO_RUNTIME_POLL_DESC_CLOSING_V1;
    }
    uint32_t previous = atomic_exchange_explicit(
        &desc->closing, 1, memory_order_seq_cst);
    return llgo_runtime_poll_desc_pack_state_v1(desc, previous);
}

static _Atomic uint64_t *llgo_runtime_poll_desc_operation_v1(
    struct llgo_runtime_poll_desc_v1 *desc,
    uint32_t interest)
{
    if (desc == NULL) {
        return NULL;
    }
    if (interest == 1) {
        return &desc->read_operation;
    }
    if (interest == 2) {
        return &desc->write_operation;
    }
    return NULL;
}

uint64_t __llgo_runtime_poll_desc_load_operation_v1(
    uintptr_t context,
    uint32_t interest)
{
    struct llgo_runtime_poll_desc_v1 *desc =
        (struct llgo_runtime_poll_desc_v1 *)context;
    _Atomic uint64_t *operation =
        llgo_runtime_poll_desc_operation_v1(desc, interest);
    if (operation == NULL) {
        return 0;
    }
    return atomic_load_explicit(operation, memory_order_seq_cst);
}

/*
 * Publish one exact, route-bearing OperationID for this fd direction.
 * Bit zero reports successful publication and bit one is the closing state
 * observed after publication. The seq_cst publish/closing handshake prevents
 * a concurrent unblock and park from both observing the other's old value.
 */
uint32_t __llgo_runtime_poll_desc_publish_operation_v1(
    uintptr_t context,
    uint32_t interest,
    uint32_t source_slot,
    uint32_t generation)
{
    struct llgo_runtime_poll_desc_v1 *desc =
        (struct llgo_runtime_poll_desc_v1 *)context;
    _Atomic uint64_t *operation =
        llgo_runtime_poll_desc_operation_v1(desc, interest);
    if (operation == NULL || source_slot == 0 || generation == 0) {
        return 0;
    }
    uint64_t desired =
        ((uint64_t)generation << 32) | (uint64_t)source_slot;
    uint64_t expected = 0;
    if (!atomic_compare_exchange_strong_explicit(
            operation,
            &expected,
            desired,
            memory_order_seq_cst,
            memory_order_seq_cst)) {
        return 0;
    }
    uint32_t closing =
        atomic_load_explicit(&desc->closing, memory_order_seq_cst);
    return UINT32_C(1) | ((closing != 0) ? UINT32_C(2) : UINT32_C(0));
}

uint32_t __llgo_runtime_poll_desc_clear_operation_v1(
    uintptr_t context,
    uint32_t interest,
    uint32_t source_slot,
    uint32_t generation)
{
    struct llgo_runtime_poll_desc_v1 *desc =
        (struct llgo_runtime_poll_desc_v1 *)context;
    _Atomic uint64_t *operation =
        llgo_runtime_poll_desc_operation_v1(desc, interest);
    if (operation == NULL || source_slot == 0 || generation == 0) {
        return 0;
    }
    uint64_t expected =
        ((uint64_t)generation << 32) | (uint64_t)source_slot;
    return atomic_compare_exchange_strong_explicit(
        operation,
        &expected,
        0,
        memory_order_seq_cst,
        memory_order_seq_cst);
}

uint32_t __llgo_runtime_poll_fd_stream_v1(int32_t fd)
{
    int saved_errno = errno;
    int socket_type = 0;
    socklen_t socket_type_size = (socklen_t)sizeof(socket_type);
    int status = getsockopt(
        (int)fd,
        SOL_SOCKET,
        SO_TYPE,
        &socket_type,
        &socket_type_size);
    uint32_t result = status == 0 &&
        socket_type_size == (socklen_t)sizeof(socket_type) &&
        socket_type == SOCK_STREAM;
    errno = saved_errno;
    return result;
}

/*
 * One bounded non-blocking I/O attempt with explicit errno transport.
 *
 * The Go owner admits these leaves only for a kernel-confirmed SOCK_STREAM.
 * Each leaf clamps its own copy length to 64 KiB, and MSG_DONTWAIT applies to
 * this one call, so the target-owned executor-safe contract remains true for
 * every size and even when shared open-file-description flags are changed
 * through dup or a raw descriptor callback. result occupies the low 32 bits
 * and errno the high 32 bits. The wrapper restores incoming C errno: no
 * executor-thread TLS state is observed after returning to Go.
 */
static uint64_t llgo_runtime_poll_pack_io_attempt_v1(ssize_t result, int error)
{
    uint32_t result_word = (uint32_t)(int32_t)result;
    uint32_t error_word = result < 0 ? (uint32_t)error : 0;
    return ((uint64_t)error_word << 32) | (uint64_t)result_word;
}

#define LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1 ((uintptr_t)64u << 10)

static size_t llgo_runtime_poll_bounded_size_v1(uintptr_t size)
{
    if (size > LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1) {
        size = LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1;
    }
    return (size_t)size;
}

uint64_t __llgo_runtime_poll_read_attempt_v1(
    int32_t fd,
    void *address,
    uintptr_t size)
{
    int saved_errno = errno;
    ssize_t result = recv(
        (int)fd,
        address,
        llgo_runtime_poll_bounded_size_v1(size),
        MSG_DONTWAIT);
    int error = result < 0 ? errno : 0;
    uint64_t packed = llgo_runtime_poll_pack_io_attempt_v1(result, error);
    errno = saved_errno;
    return packed;
}

uint64_t __llgo_runtime_poll_write_attempt_v1(
    int32_t fd,
    void *address,
    uintptr_t size)
{
    int saved_errno = errno;
    ssize_t result = send(
        (int)fd,
        address,
        llgo_runtime_poll_bounded_size_v1(size),
        MSG_DONTWAIT);
    int error = result < 0 ? errno : 0;
    uint64_t packed = llgo_runtime_poll_pack_io_attempt_v1(result, error);
    errno = saved_errno;
    return packed;
}

/*
 * Fixed scalar ABI for the llgo.syscall worker intrinsic.
 *
 * Darwin's nfds_t is unsigned int while Linux uses unsigned long, and poll
 * returns int. Keeping those platform types behind this wrapper lets the
 * worker call one exact uintptr-only signature on both systems. timeout_word
 * carries the low 32 bits of Go's int32 C.int; the explicit unsigned then
 * signed conversion restores -1 without depending on uintptr width.
 */
uintptr_t __llgo_runtime_poll_wait_v1(
    uintptr_t fds_address,
    uintptr_t nfds_word,
    uintptr_t timeout_word) {
    struct pollfd *fds = (struct pollfd *)(uintptr_t)fds_address;
    nfds_t nfds = (nfds_t)nfds_word;
    int timeout = (int)(int32_t)(uint32_t)timeout_word;
    int result = poll(fds, nfds, timeout);
    if (result < 0) {
        return UINTPTR_MAX;
    }
    return (uintptr_t)(unsigned int)result;
}

#endif
