#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdatomic.h>

// === Skink Automatic Reference Counting (ARC) Runtime ===
//
// Every heap-allocated object has a 24-byte prefix:
//   [0..7]  atomic refcount (int64)
//   [8..15] magic number   (i64)
//   [16..23] user data size in bytes (i64)
//   [24..]  payload
//
// All public functions work with the *payload* pointer (not the raw block).
// retain/release are safe to call on non-RC pointers (string literals,
// C-library strings, null, etc.) — they simply return early.

#define RC_PREFIX_SIZE 24
#define RC_MAGIC 0xDEADBEEFCAFEBABEULL

typedef struct {
    atomic_int_fast64_t count;
    int64_t magic;
    int64_t size;
} RCHeader;

static inline int is_rc_ptr(void* ptr) {
    if (!ptr) return 0;
    RCHeader* h = (RCHeader*)((char*)ptr - RC_PREFIX_SIZE);
    return h->magic == RC_MAGIC;
}

void* Skink_rc_alloc(int64_t payload_size) {
    int64_t total = RC_PREFIX_SIZE + payload_size;
    void* raw = malloc(total);
    if (!raw) return NULL;
    memset(raw, 0, total);
    RCHeader* h = (RCHeader*)raw;
    atomic_store(&h->count, 1);
    h->magic = RC_MAGIC;
    h->size = payload_size;
    return (char*)raw + RC_PREFIX_SIZE;
}

void* Skink_rc_retain(void* ptr) {
    if (!is_rc_ptr(ptr)) return ptr;
    RCHeader* h = (RCHeader*)((char*)ptr - RC_PREFIX_SIZE);
    atomic_fetch_add(&h->count, 1);
    return ptr;
}

void Skink_rc_release(void* ptr) {
    if (!is_rc_ptr(ptr)) return;
    RCHeader* h = (RCHeader*)((char*)ptr - RC_PREFIX_SIZE);
    int64_t prev = atomic_fetch_sub(&h->count, 1);
    if (prev == 1) {
        free(h);
    }
}

int64_t Skink_rc_count(void* ptr) {
    if (!is_rc_ptr(ptr)) return 0;
    RCHeader* h = (RCHeader*)((char*)ptr - RC_PREFIX_SIZE);
    return atomic_load(&h->count);
}
