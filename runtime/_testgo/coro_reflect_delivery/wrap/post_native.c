#include <pthread.h>
#include <stdatomic.h>

static pthread_mutex_t post_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t post_ready = PTHREAD_COND_INITIALIZER;
static _Atomic int enabled, entered, released;
void llgo_delivery_mark_waiting(void);

void llgo_reflect_post_native_enable(void) { atomic_store(&enabled, 1); }
int llgo_reflect_post_native_entered(void) { return atomic_load(&entered); }

/* Called only by the test's private source copy, immediately after real native
   invocation/FinishInvocation and before result boxing. Production has no hook. */
void llgo_reflect_post_native_wait(void) {
    if (!atomic_load(&enabled)) return;
    pthread_mutex_lock(&post_lock);
    llgo_delivery_mark_waiting();
    atomic_store(&entered, 1);
    while (!atomic_load(&released)) pthread_cond_wait(&post_ready, &post_lock);
    pthread_mutex_unlock(&post_lock);
}

void llgo_reflect_post_native_release(void) {
    pthread_mutex_lock(&post_lock);
    atomic_store(&released, 1);
    pthread_cond_broadcast(&post_ready);
    pthread_mutex_unlock(&post_lock);
}

void llgo_delivery_release(void);

void llgo_reflect_release_waits(void) {
    llgo_delivery_release();
    llgo_reflect_post_native_release();
}
