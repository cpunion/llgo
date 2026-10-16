// Exercise the actual reactor queue with real WASI file descriptors, without
// compiling a second scheduler or mocking the source-generation protocol.
#include <wasi/api.h>

static int fail_flags_descriptor = -1;
static unsigned failed_flags_calls;

// Force one metadata failure without replacing real descriptor metadata or I/O.
// A host failure on a descriptor with mutable flags must not be swallowed.
static __wasi_errno_t checked_set_flags(__wasi_fd_t descriptor, __wasi_fdflags_t flags)
{
    if ((int)descriptor == fail_flags_descriptor) {
        failed_flags_calls++;
        return __WASI_ERRNO_IO;
    }
    return __wasi_fd_fdstat_set_flags(descriptor, flags);
}

#define __wasi_fd_fdstat_set_flags checked_set_flags
#include "../../../../runtime/internal/runtime/_wrap/coro_wasi_command.c"
#undef __wasi_fd_fdstat_set_flags

static uint32_t completed_count;
static uint32_t completed_errno[16];
static uint32_t completed_result[16];
static uint32_t completed_scalar[16];
static __wasi_iovec_t operation_vectors[16];
static __wasi_size_t operation_results[16];

uint32_t __llgo_coro_host_complete_operation_v1(
    uint32_t slot, uint32_t generation, uint32_t reserved, uint32_t words,
    uint32_t r1, uint32_t r1_hi, uint32_t r2, uint32_t r2_hi,
    uint32_t error, uint32_t error_hi)
{
    if (slot == 0 || slot >= 16 || generation == 0 || reserved != 0 ||
        words != 3 || r1_hi != 0 || r2 != 0 || r2_hi != 0 || error_hi != 0) {
        return 0;
    }
    completed_count++;
    completed_errno[slot] = error;
    completed_scalar[slot] = r1;
    completed_result[slot] = operation_results[slot];
    return 1;
}

static struct llgo_host_operation_v1 positioned(
    uint32_t slot, uint32_t generation, uint32_t opcode,
    int descriptor, void *buffer, uint32_t length, uint64_t offset)
{
    struct llgo_host_operation_v1 operation = {0};
    operation.kind = llgo_host_operation_submit_v1;
    operation.source_slot = slot;
    operation.source_generation = generation;
    operation.opcode = opcode;
    bool positioned = opcode == llgo_host_wasi_fd_pread_v1 ||
                      opcode == llgo_host_wasi_fd_pwrite_v1;
    operation.arg_count = positioned ? 6 : 4;
    operation_vectors[slot].buf = buffer;
    operation_vectors[slot].buf_len = length;
    operation_results[slot] = 0;
    operation.args[0] = (uint32_t)descriptor;
    operation.args[2] = (uint32_t)(uintptr_t)&operation_vectors[slot];
    operation.args[4] = 1;
    if (positioned) {
        operation.args[6] = (uint32_t)offset;
        operation.args[8] = (uint32_t)(offset >> 32);
        operation.args[10] = (uint32_t)(uintptr_t)&operation_results[slot];
    } else {
        operation.args[6] = (uint32_t)(uintptr_t)&operation_results[slot];
    }
    return operation;
}

static struct llgo_host_operation_v1 buffered(
    uint32_t slot, uint32_t generation, uint32_t opcode,
    int descriptor, void *buffer, uint32_t length)
{
    struct llgo_host_operation_v1 operation = {0};
    operation.kind = llgo_host_operation_submit_v1;
    operation.source_slot = slot;
    operation.source_generation = generation;
    operation.opcode = opcode;
    operation.arg_count = 3;
    operation.args[0] = (uint32_t)descriptor;
    operation.args[2] = (uint32_t)(uintptr_t)buffer;
    operation.args[4] = length;
    return operation;
}

int main(void)
{
    const char *path = "/tmp/llgo-wasi-reactor-operation-test";
    int descriptor = open(path, O_CREAT | O_TRUNC | O_RDWR, 0600);
    if (descriptor < 0 || write(descriptor, "abc", 3) != 3) return 1;
    fail_flags_descriptor = descriptor;
    if (llgo_make_nonblocking_v1(descriptor) != __WASI_ERRNO_IO ||
        failed_flags_calls != 1) return 19;
    fail_flags_descriptor = -1;
    char buffer[4] = "XY";
    struct llgo_host_operation_v1 operation = positioned(
        1, 7, llgo_host_wasi_fd_pwrite_v1, descriptor, buffer, 2, 1);
    if (!llgo_take_operation_v1(&operation)) return 2;
    operation.kind = llgo_host_operation_cancel_v1;
    if (!llgo_take_operation_v1(&operation) || completed_count != 1 ||
        completed_errno[1] != ECANCELED || llgo_find_operation_v1(1, 7)) return 3;
    char contents[3];
    if (pread(descriptor, contents, 3, 0) != 3 || memcmp(contents, "abc", 3)) return 4;

    operation.kind = llgo_host_operation_submit_v1;
    operation.source_generation++;
    if (!llgo_take_operation_v1(&operation)) return 5;
    struct llgo_pending_operation_v1 *pending = llgo_find_operation_v1(1, 8);
    if (!pending || !llgo_service_ready_operation_v1(pending) || pending->active ||
        completed_count != 2 || completed_errno[1] || completed_result[1] != 2) return 6;
    if (lseek(descriptor, 0, SEEK_CUR) != 3 ||
        pread(descriptor, contents, 3, 0) != 3 || memcmp(contents, "aXY", 3)) return 7;

    operation = positioned(2, 9, llgo_host_wasi_fd_pread_v1, descriptor, buffer, 4, 2);
    if (!llgo_take_operation_v1(&operation)) return 8;
    pending = llgo_find_operation_v1(2, 9);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        completed_errno[2] || completed_result[2] != 1 || buffer[0] != 'Y') return 9;

    // The deadline-aware internal/poll adapter uses the generic buffer ABI,
    // sharing exactly the raw-WASI pending queue rather than a second reactor.
    if (lseek(descriptor, 0, SEEK_SET)) return 40;
    operation = buffered(13, 20, llgo_host_file_read_v1, descriptor, buffer, 4);
    if (!llgo_take_operation_v1(&operation)) return 41;
    pending = llgo_find_operation_v1(13, 20);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        completed_errno[13] || completed_scalar[13] != 3 || memcmp(buffer, "aXY", 3)) return 42;
    operation.source_generation++;
    if (!llgo_take_operation_v1(&operation)) return 43;
    pending = llgo_find_operation_v1(13, 21);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        completed_errno[13] || completed_scalar[13] != 0) return 44;
    operation = buffered(14, 22, llgo_host_file_write_v1, descriptor, "!", 1);
    if (!llgo_take_operation_v1(&operation)) return 45;
    operation.kind = llgo_host_operation_cancel_v1;
    if (!llgo_take_operation_v1(&operation) || completed_errno[14] != ECANCELED ||
        completed_scalar[14] != UINT32_MAX || lseek(descriptor, 0, SEEK_END) != 3) return 46;
    operation.kind = llgo_host_operation_submit_v1;
    operation.source_generation++;
    if (!llgo_take_operation_v1(&operation)) return 47;
    pending = llgo_find_operation_v1(14, 23);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        completed_errno[14] || completed_scalar[14] != 1) return 48;
    operation.source_generation++;
    if (!llgo_take_operation_v1(&operation)) return 49;

    // Closing a descriptor must retire delivered pending transfers before the
    // integer can be reused for an unrelated file.
    operation = positioned(3, 10, llgo_host_wasi_fd_pwrite_v1, descriptor, buffer, 1, 0);
    if (!llgo_take_operation_v1(&operation)) return 10;
    struct llgo_host_operation_v1 close_operation = {0};
    close_operation.kind = llgo_host_operation_submit_v1;
    close_operation.source_slot = 4;
    close_operation.source_generation = 11;
    close_operation.opcode = llgo_host_wasi_fd_close_v1;
    close_operation.arg_count = 1;
    close_operation.args[0] = (uint32_t)descriptor;
    if (!llgo_take_operation_v1(&close_operation) || llgo_find_operation_v1(3, 10) ||
        completed_errno[3] != EBADF || completed_errno[4] ||
        llgo_find_operation_v1(14, 24) || completed_errno[14] != EBADF) return 11;
    if (llgo_make_nonblocking_v1(descriptor) != __WASI_ERRNO_BADF) return 20;

    descriptor = open(path, O_RDONLY);
    if (descriptor < 0) return 21;
    operation = positioned(6, 13, llgo_host_wasi_fd_write_v1, descriptor, buffer, 1, 0);
    if (!llgo_take_operation_v1(&operation)) return 22;
    pending = llgo_find_operation_v1(6, 13);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        pending->active || completed_errno[6] != EBADF) return 23;
    if (close(descriptor) != 0) return 24;

    descriptor = open(path, O_WRONLY);
    if (descriptor < 0) return 25;
    operation = positioned(7, 14, llgo_host_wasi_fd_read_v1, descriptor, buffer, 1, 0);
    if (!llgo_take_operation_v1(&operation)) return 26;
    pending = llgo_find_operation_v1(7, 14);
    if (!pending || !llgo_service_ready_operation_v1(pending) ||
        pending->active || completed_errno[7] != EBADF) return 27;
    if (close(descriptor) != 0) return 28;

    // Raw paths preserve explicit length, including embedded NUL rejection;
    // wasi-libc's strlen wrappers would incorrectly accept this as the file.
    int directory = open("/tmp", O_RDONLY | O_DIRECTORY);
    if (directory < 0) return 30;
    char nul_path[] = "llgo-wasi-reactor-operation-test\0hidden";
    __wasi_filestat_t stat;
    operation = (struct llgo_host_operation_v1){0};
    operation.kind = llgo_host_operation_submit_v1;
    operation.source_slot = 9;
    operation.source_generation = 16;
    operation.opcode = llgo_host_wasi_path_filestat_get_v1;
    operation.arg_count = 5;
    operation.args[0] = (uint32_t)directory;
    operation.args[4] = (uint32_t)(uintptr_t)nul_path;
    operation.args[6] = sizeof(nul_path) - 1;
    operation.args[8] = (uint32_t)(uintptr_t)&stat;
    if (!llgo_take_operation_v1(&operation) || !completed_errno[9]) return 31;

    uint64_t rights[2] = { __WASI_RIGHTS_FD_READ | __WASI_RIGHTS_FD_WRITE, 0 };
    __wasi_fd_t opened = -1;
    char raw_name[] = "raw-file-ignored-tail";
    operation.source_slot = 10;
    operation.source_generation = 17;
    operation.opcode = llgo_host_wasi_path_open_v1;
    operation.arg_count = 8;
    operation.args[4] = (uint32_t)(uintptr_t)raw_name;
    operation.args[6] = 8;
    operation.args[8] = __WASI_OFLAGS_CREAT;
    operation.args[10] = (uint32_t)(uintptr_t)rights;
    operation.args[14] = (uint32_t)(uintptr_t)&opened;
    if (!llgo_take_operation_v1(&operation) || completed_errno[10] || opened < 0) return 32;
    if (close(opened) || unlink("/tmp/raw-file")) return 33;
    operation.source_slot = 11;
    operation.source_generation = 18;
    operation.args[10] = 0;
    if (!llgo_take_operation_v1(&operation) || completed_errno[11] != EFAULT) return 34;
    operation.source_slot = 12;
    operation.source_generation = 19;
    operation.args[1] = 1; // upper word of a wasm32 fd must not be truncated
    if (!llgo_take_operation_v1(&operation) || completed_errno[12] != EINVAL) return 35;
    if (close(directory)) return 36;
    if (unlink(path) != 0) return 12;

    // Inherited stdout is a host stream, not an opened regular file. Exercise
    // its real fd metadata and poll behavior as well as the direct file path.
    char message[] = "reactor stdout ok\n";
    operation = positioned(8, 15, llgo_host_wasi_fd_write_v1, 1,
                           message, sizeof(message) - 1, 0);
    if (!llgo_take_operation_v1(&operation) || !llgo_service_unpollable_files_v1() ||
        completed_errno[8] != ENOTSUP || llgo_find_operation_v1(8, 15)) return 29;
    operation = positioned(5, 12, llgo_host_wasi_fd_write_v1, 1,
                           message, sizeof(message) - 1, 0);
    if (!llgo_take_operation_v1(&operation)) return 13;
    uint32_t count = 0;
    if (!llgo_build_poll_set_v1(&count)) return 14;
    if (count != 1) return 15;
    __wasi_size_t event_count = 0;
    __wasi_errno_t error = __wasi_poll_oneoff(
        llgo_wasi_subscriptions_v1, llgo_wasi_events_v1, count, &event_count);
    if (error == __WASI_ERRNO_NOTSUP) {
        if (!llgo_service_unpollable_files_v1()) return 16;
    } else if (error || event_count != 1 || !llgo_service_poll_events_v1(event_count)) {
        return 17;
    }
    if (completed_errno[5] || completed_result[5] != sizeof(message) - 1) return 18;
    return 0;
}
