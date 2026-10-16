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

#include <gc/gc.h>
#include <stddef.h>
#include <stdint.h>
#if defined(__unix__) || defined(__APPLE__)
#include <errno.h>
#include <unistd.h>
#endif

/* All fields are configured under the collector allocation lock. The epoch
 * points to process-lifetime static storage, not a managed heap object. The
 * callback never takes a Go lock, allocates, or enters a scheduler/Go callback.
 * Its write fd is the existing coordinator doorbell, detached under the same
 * collector lock before either end of that pipe is closed or reused. */
static uint32_t *llgo_gc_epoch;
static int32_t llgo_gc_wake_fd = -1;
static GC_on_collection_event_proc llgo_gc_previous_event;

static void GC_CALLBACK llgo_gc_collection_event(GC_EventType event) {
  if (event == GC_EVENT_END && llgo_gc_epoch != NULL) {
    __atomic_fetch_add(llgo_gc_epoch, 1, __ATOMIC_RELEASE);
#if defined(__unix__) || defined(__APPLE__)
    if (llgo_gc_wake_fd >= 0) {
      int saved_errno = errno;
      const unsigned char byte = 0;
      /* O_NONBLOCK is guaranteed by the existing doorbell constructor.
       * EAGAIN means a retained byte already exists; all other failures are
       * covered by the owner's retained epoch and bounded poll fallback. */
      for (int attempt = 0; attempt < 4; attempt++) {
        if (write(llgo_gc_wake_fd, &byte, 1) >= 0 || errno != EINTR)
          break;
      }
      errno = saved_errno;
    }
#endif
  }
  if (llgo_gc_previous_event != NULL)
    llgo_gc_previous_event(event);
}

/* Called once during the allocator bootstrap, before executor/worker threads
 * start. Preserve an embedder's existing callback. These GC setters themselves
 * take the allocation lock, so they must not run inside call_with_alloc_lock. */
void __llgo_coro_gc_notification_init_v1(void) {
  GC_on_collection_event_proc previous = GC_get_on_collection_event();
  if (previous != llgo_gc_collection_event) {
    llgo_gc_previous_event = previous;
    GC_set_on_collection_event(llgo_gc_collection_event);
  }
}

struct llgo_gc_notification_config {
  uint32_t *epoch;
  int32_t fd;
};

static void *llgo_gc_notification_enable_locked(void *raw) {
  struct llgo_gc_notification_config *config = raw;
  llgo_gc_epoch = config->epoch;
  llgo_gc_wake_fd = config->fd;
  return NULL;
}

void __llgo_coro_gc_notification_enable_v1(uint32_t *epoch, int32_t fd) {
  struct llgo_gc_notification_config config = {epoch, fd};
  GC_call_with_alloc_lock(llgo_gc_notification_enable_locked, &config);
}

static void *llgo_gc_notification_detach_locked(void *raw) {
  if (llgo_gc_wake_fd == *(int32_t *)raw)
    llgo_gc_wake_fd = -1;
  return NULL;
}

void __llgo_coro_gc_notification_detach_v1(int32_t fd) {
  GC_call_with_alloc_lock(llgo_gc_notification_detach_locked, &fd);
}
