#define _GNU_SOURCE 1
#include <pthread.h>
#include <stdint.h>
#include <stdatomic.h>
#include <errno.h>
#include <sched.h>

static int probe_pthread_create(pthread_t *, const pthread_attr_t *,
                               void *(*)(void *), void *);

/* Keep the production queue, worker routine and create wrapper intact. */
#define pthread_create probe_pthread_create
#include "worker_impl.h"
#undef pthread_create

static _Atomic uint32_t attempts;
static _Atomic uint32_t successes;
static _Atomic uint32_t joins;
static _Atomic uint32_t entered;
static _Atomic uint32_t completed;
static _Atomic uint32_t bad_completions;
static _Atomic uint32_t fail_at;
static _Atomic uint32_t hold_at;
static _Atomic bool release_create;
static _Atomic bool release_calls;
static uintptr_t failed_output_sentinel;

static int probe_pthread_create(pthread_t *thread, const pthread_attr_t *attr,
                               void *(*routine)(void *), void *argument) {
    uint32_t attempt = atomic_fetch_add(&attempts, 1) + 1;
    while (attempt == atomic_load(&hold_at) && !atomic_load(&release_create))
        sched_yield();
    if (attempt == atomic_load(&fail_at)) {
        /* A failed pthread_create does not promise a usable output slot. */
        *thread = (pthread_t)(uintptr_t)&failed_output_sentinel;
        return EAGAIN;
    }
    int result = pthread_create(thread, attr, routine, argument);
    if (result == 0) atomic_fetch_add(&successes, 1);
    return result;
}

int probe_create(uintptr_t *thread) {
    pthread_t native = (pthread_t)(uintptr_t)0;
    int result = __llgo_coro_worker_create_v1(&native);
    *thread = (uintptr_t)native;
    return result;
}

int probe_join(uintptr_t thread) {
    void *result = (void *)(uintptr_t)1;
    int error = pthread_join((pthread_t)thread, &result);
    if (error == 0 && result == NULL) atomic_fetch_add(&joins, 1);
    return error != 0 ? error : result != NULL;
}

static uintptr_t probe_blocked_call(void) {
    atomic_fetch_add(&entered, 1);
    while (!atomic_load(&release_calls)) sched_yield();
    return 123;
}

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t slot, uint32_t generation, uintptr_t r1, uintptr_t r2,
    uintptr_t error, uintptr_t fault, uintptr_t pc, uintptr_t target) {
    (void)r2; (void)error; (void)pc; (void)target;
    if (slot != UINT32_C(0x05008001) || generation < 1 || generation > 4 ||
        r1 != 123 || fault != 0) {
        atomic_fetch_add(&bad_completions, 1);
        return 0;
    }
    atomic_fetch_add(&completed, 1);
    return 1;
}

bool probe_submit(uint32_t generation) {
    size_t token = __llgo_coro_worker_queue_reserve_v2();
    return token != 0 && __llgo_coro_worker_queue_submit_reserved_v4(
        token, UINT32_C(0x05008001), generation,
        (uintptr_t)probe_blocked_call, (uintptr_t)probe_blocked_call, 0,
        0, 0, 0, 0, 0, 0, 0, 0, 0);
}

void probe_fail_at(uint32_t attempt) { atomic_store(&fail_at, attempt); }
void probe_hold_at(uint32_t attempt) { atomic_store(&hold_at, attempt); }
void probe_release_create(void) { atomic_store(&release_create, true); }
void probe_release_calls(void) { atomic_store(&release_calls, true); }
uint32_t probe_attempts(void) { return atomic_load(&attempts); }
uint32_t probe_successes(void) { return atomic_load(&successes); }
uint32_t probe_joins(void) { return atomic_load(&joins); }
uint32_t probe_entered(void) { return atomic_load(&entered); }
uint32_t probe_completed(void) { return atomic_load(&completed); }
uint32_t probe_bad_completions(void) { return atomic_load(&bad_completions); }
