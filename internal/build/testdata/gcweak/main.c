#include <gc/gc.h>
#include <gc/gc_mark.h>
#include <assert.h>
#include <stdint.h>

extern int __llgo_coro_gc_register_weak_v1(void *, uintptr_t *);
extern void *__llgo_coro_gc_weak_strong_v1(uintptr_t, uintptr_t *);
extern void *__llgo_coro_gc_weak_base_v1(void *);

struct weak_cell { uintptr_t key, live; };
static struct weak_cell *cell;
static int finalized;
static int global;

static void finalize(void *object, void *client) {
  (void)object;
  struct weak_cell *weak = client;
  assert(weak->live == 0);
  assert(__llgo_coro_gc_weak_strong_v1(weak->key, &weak->live) == NULL);
  finalized++;
}

__attribute__((noinline)) static void allocate_referent(void) {
  char *object = GC_malloc(4096);
  assert(object != NULL);
  void *interior = object + 37;
  cell->key = ~(uintptr_t)interior;
  cell->live = 1;
  assert(__llgo_coro_gc_weak_base_v1(interior) == object);
  assert(__llgo_coro_gc_register_weak_v1(interior, &cell->live));
  assert(__llgo_coro_gc_weak_strong_v1(cell->key, &cell->live) == interior);
  GC_register_finalizer_no_order(object, finalize, cell, NULL, NULL);
  GC_reachable_here(object);
}

__attribute__((noinline)) static void scrub_and_collect(void) {
  volatile uintptr_t scratch[8192];
  for (unsigned i = 0; i < sizeof(scratch) / sizeof(scratch[0]); i++)
    scratch[i] = 0;
  GC_clear_stack(NULL);
  GC_gcollect();
  GC_reachable_here(scratch);
}

int main(void) {
  GC_INIT();
  GC_set_finalize_on_demand(1);
  uintptr_t permanent = 1;
  assert(__llgo_coro_gc_weak_base_v1(&global) == NULL);
  assert(__llgo_coro_gc_register_weak_v1(&global, &permanent));
  assert(__llgo_coro_gc_weak_strong_v1(~(uintptr_t)&global, &permanent) == &global);

  cell = GC_malloc_uncollectable(sizeof(*cell));
  allocate_referent();
  for (int attempt = 0; attempt < 32 && cell->live; attempt++) {
    scrub_and_collect();
  }
  assert(cell->live == 0);
  assert(finalized == 0); /* clearing must not depend on a finalizer drain */
  assert(__llgo_coro_gc_weak_strong_v1(cell->key, &cell->live) == NULL);
  GC_invoke_finalizers();
  assert(finalized == 1);
  return 0;
}
