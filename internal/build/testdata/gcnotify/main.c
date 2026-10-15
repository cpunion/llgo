#define GC_THREADS 1
#include <gc/gc.h>
#include <errno.h>
#include <poll.h>
#include <pthread.h>
#include <stdint.h>
#include <unistd.h>

void __llgo_coro_gc_notification_init_v1(void);
void __llgo_coro_gc_notification_enable_v1(uint32_t *, int32_t);
void __llgo_coro_gc_notification_detach_v1(int32_t);
int32_t __llgo_coro_doorbell_open_v1(int32_t[2]);

static uint32_t epoch, previous_calls;

static void previous(GC_EventType event) {
  if (event == GC_EVENT_END)
    __atomic_fetch_add(&previous_calls, 1, __ATOMIC_RELEASE);
}

static void *collect_automatically(void *unused) {
  (void)unused;
  struct GC_stack_base stack;
  if (GC_get_stack_base(&stack) != GC_SUCCESS)
    return (void *)1;
  int registration = GC_register_my_thread(&stack);
  if (registration != GC_SUCCESS && registration != GC_DUPLICATE)
    return (void *)2;
  for (int i = 0; i < 1024 && __atomic_load_n(&epoch, __ATOMIC_ACQUIRE) == 0; i++) {
    /* No GC_gcollect: the test must exercise allocation-triggered GC on a
     * registered foreign thread, not an explicit Go runtime.GC wrapper. */
    volatile unsigned char *block = GC_malloc(1024 * 1024);
    if (block == NULL)
      return (void *)3;
    block[0] = 1;
    GC_reachable_here(block);
  }
  if (registration == GC_SUCCESS)
    GC_unregister_my_thread();
  return NULL;
}

int main(void) {
  GC_init();
  GC_allow_register_threads();
  GC_set_on_collection_event(previous);
  __llgo_coro_gc_notification_init_v1();
  __llgo_coro_gc_notification_init_v1(); /* must not chain to itself */
  GC_gcollect();
  if (epoch != 0 || previous_calls == 0)
    return 10;

  int32_t fds[2];
  if (__llgo_coro_doorbell_open_v1(fds) != 1)
    return 11;
  __llgo_coro_gc_notification_enable_v1(&epoch, fds[1]);
  pthread_t producer;
  if (pthread_create(&producer, NULL, collect_automatically, NULL) != 0)
    return 12;
  struct pollfd wake = {fds[0], POLLIN, 0};
  int ready;
  do { ready = poll(&wake, 1, 5000); } while (ready < 0 && errno == EINTR);
  void *result;
  if (pthread_join(producer, &result) != 0 || result != NULL || ready != 1 ||
      !(wake.revents & POLLIN) || __atomic_load_n(&epoch, __ATOMIC_ACQUIRE) == 0)
    return 13;

  /* Detach is a strong collector-lock join. Reusing the very same descriptor
   * number must not allow later collections to write into an unrelated pipe. */
  __llgo_coro_gc_notification_detach_v1(fds[1]);
  if (close(fds[0]) || close(fds[1]))
    return 14;
  int32_t replacement[2];
  if (__llgo_coro_doorbell_open_v1(replacement) != 1)
    return 15;
  if (replacement[1] != fds[1] && dup2(replacement[1], fds[1]) < 0)
    return 16;
  uint32_t before = __atomic_load_n(&epoch, __ATOMIC_ACQUIRE);
  GC_gcollect();
  unsigned char byte;
  if (__atomic_load_n(&epoch, __ATOMIC_ACQUIRE) == before ||
      read(replacement[0], &byte, 1) != -1 || (errno != EAGAIN && errno != EWOULDBLOCK))
    return 17;
  if (replacement[1] != fds[1])
    close(fds[1]);
  close(replacement[0]);
  close(replacement[1]);
  return 0;
}
