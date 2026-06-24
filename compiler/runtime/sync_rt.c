// Copyright 2026 Mark Oxley
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// sync_rt.c provides C wrappers around POSIX pthread synchronization primitives
// and GCC atomic built-ins.  This allows the Skink std/sync module to use
// robust threading primitives without needing to know the size or layout of
// opaque C types such as pthread_mutex_t.
//
// Compile and link this file alongside any Skink program that imports std/sync.

#include <stdlib.h>
#include <pthread.h>

// ---------------------------------------------------------------------------
// Mutex
// ---------------------------------------------------------------------------

// Allocates and initialises a pthread_mutex_t on the heap.  Returns an opaque
// pointer that should be cast to int inside Skink code.
void* Skink_mutex_alloc(void) {
    pthread_mutex_t* mu = (pthread_mutex_t*)calloc(1, sizeof(pthread_mutex_t));
    pthread_mutex_init(mu, NULL);
    return mu;
}

// Acquire the mutex.  Blocks until the mutex is available.
void Skink_mutex_lock(void* mu) {
    pthread_mutex_lock((pthread_mutex_t*)mu);
}

// Attempt to acquire the mutex without blocking.  Returns 0 on success,
// non-zero if the mutex is already held.
int Skink_mutex_trylock(void* mu) {
    return pthread_mutex_trylock((pthread_mutex_t*)mu);
}

// Release the mutex.
void Skink_mutex_unlock(void* mu) {
    pthread_mutex_unlock((pthread_mutex_t*)mu);
}

// Destroy and free a mutex allocated by Skink_mutex_alloc.
void Skink_mutex_free(void* mu) {
    pthread_mutex_destroy((pthread_mutex_t*)mu);
    free(mu);
}

// ---------------------------------------------------------------------------
// Read-Write Lock (RWMutex)
// ---------------------------------------------------------------------------

void* Skink_rwlock_alloc(void) {
    pthread_rwlock_t* rw = (pthread_rwlock_t*)calloc(1, sizeof(pthread_rwlock_t));
    pthread_rwlock_init(rw, NULL);
    return rw;
}

void Skink_rwlock_rdlock(void* rw) {
    pthread_rwlock_rdlock((pthread_rwlock_t*)rw);
}

void Skink_rwlock_wrlock(void* rw) {
    pthread_rwlock_wrlock((pthread_rwlock_t*)rw);
}

void Skink_rwlock_unlock(void* rw) {
    pthread_rwlock_unlock((pthread_rwlock_t*)rw);
}

int Skink_rwlock_tryrdlock(void* rw) {
    return pthread_rwlock_tryrdlock((pthread_rwlock_t*)rw);
}

int Skink_rwlock_trywrlock(void* rw) {
    return pthread_rwlock_trywrlock((pthread_rwlock_t*)rw);
}

void Skink_rwlock_free(void* rw) {
    pthread_rwlock_destroy((pthread_rwlock_t*)rw);
    free(rw);
}

// ---------------------------------------------------------------------------
// Condition Variable
// ---------------------------------------------------------------------------

void* Skink_cond_alloc(void) {
    pthread_cond_t* cv = (pthread_cond_t*)calloc(1, sizeof(pthread_cond_t));
    pthread_cond_init(cv, NULL);
    return cv;
}

void Skink_cond_wait(void* cv, void* mu) {
    pthread_cond_wait((pthread_cond_t*)cv, (pthread_mutex_t*)mu);
}

void Skink_cond_signal(void* cv) {
    pthread_cond_signal((pthread_cond_t*)cv);
}

void Skink_cond_broadcast(void* cv) {
    pthread_cond_broadcast((pthread_cond_t*)cv);
}

void Skink_cond_free(void* cv) {
    pthread_cond_destroy((pthread_cond_t*)cv);
    free(cv);
}

// ---------------------------------------------------------------------------
// Atomic operations (GCC built-in wrappers)
//
// __atomic_* are compiler intrinsics; they do not exist as library symbols.
// By wrapping them in small C functions we expose real linker symbols that
// Skink can call via extern fn declarations.
// ---------------------------------------------------------------------------

int Skink_atomic_load(int* ptr) {
    return __atomic_load_n(ptr, __ATOMIC_SEQ_CST);
}

void Skink_atomic_store(int* ptr, int val) {
    __atomic_store_n(ptr, val, __ATOMIC_SEQ_CST);
}

int Skink_atomic_add(int* ptr, int delta) {
    return __atomic_add_fetch(ptr, delta, __ATOMIC_SEQ_CST);
}

int Skink_atomic_sub(int* ptr, int delta) {
    return __atomic_sub_fetch(ptr, delta, __ATOMIC_SEQ_CST);
}

int Skink_atomic_exchange(int* ptr, int val) {
    return __atomic_exchange_n(ptr, val, __ATOMIC_SEQ_CST);
}

// Compare-and-swap.  Returns 1 (true) if the swap succeeded, 0 (false) otherwise.
int Skink_atomic_cas(int* ptr, int* expected, int desired) {
    return __atomic_compare_exchange_n(
        ptr, expected, desired, 0,
        __ATOMIC_SEQ_CST, __ATOMIC_SEQ_CST);
}
