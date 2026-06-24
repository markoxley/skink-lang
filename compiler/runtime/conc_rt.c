#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>

// === Channel implementation ===

typedef struct ChanNode {
    void* data;
    struct ChanNode* next;
} ChanNode;

typedef struct {
    int elem_size;
    int capacity;
    int closed;
    ChanNode* head;
    ChanNode* tail;
    int count;
    pthread_mutex_t mu;
    pthread_cond_t send_cv;
    pthread_cond_t recv_cv;
} Chan;

void* Skink_chan_make(int elem_size, int capacity) {
    Chan* ch = calloc(1, sizeof(Chan));
    ch->elem_size = elem_size;
    ch->capacity = capacity;
    pthread_mutex_init(&ch->mu, NULL);
    pthread_cond_init(&ch->send_cv, NULL);
    pthread_cond_init(&ch->recv_cv, NULL);
    return ch;
}

void Skink_chan_send(void* ch_ptr, void* val) {
    Chan* ch = (Chan*)ch_ptr;
    pthread_mutex_lock(&ch->mu);
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        return;
    }
    ChanNode* node = malloc(sizeof(ChanNode));
    node->data = malloc(ch->elem_size);
    memcpy(node->data, val, ch->elem_size);
    node->next = NULL;
    if (ch->tail) {
        ch->tail->next = node;
        ch->tail = node;
    } else {
        ch->head = ch->tail = node;
    }
    ch->count++;
    pthread_cond_signal(&ch->recv_cv);
    pthread_mutex_unlock(&ch->mu);
}

void* Skink_chan_recv(void* ch_ptr) {
    Chan* ch = (Chan*)ch_ptr;
    pthread_mutex_lock(&ch->mu);
    while (ch->head == NULL && !ch->closed) {
        pthread_cond_wait(&ch->recv_cv, &ch->mu);
    }
    if (ch->head == NULL) {
        pthread_mutex_unlock(&ch->mu);
        return NULL;
    }
    ChanNode* node = ch->head;
    void* data = node->data;
    ch->head = node->next;
    if (ch->head == NULL) ch->tail = NULL;
    ch->count--;
    free(node);
    pthread_cond_signal(&ch->send_cv);
    pthread_mutex_unlock(&ch->mu);
    return data;
}

void Skink_chan_close(void* ch_ptr) {
    Chan* ch = (Chan*)ch_ptr;
    pthread_mutex_lock(&ch->mu);
    ch->closed = 1;
    pthread_cond_broadcast(&ch->recv_cv);
    pthread_cond_broadcast(&ch->send_cv);
    pthread_mutex_unlock(&ch->mu);
}

int Skink_chan_select(int n, void** chs, int* is_send, void** vals, void** out_val, int timeout_ms) {
    // Simple select: poll each channel once in order.
    // For receive: check if data is available.
    // For send: always succeed (unbounded channels).
    int ready_idx = -1;
    int loops = 0;
    while (ready_idx == -1 && loops < 10000) {
        for (int i = 0; i < n; i++) {
            Chan* ch = (Chan*)chs[i];
            if (is_send[i]) {
                // Send always succeeds on unbounded channel.
                ready_idx = i;
                break;
            } else {
                pthread_mutex_lock(&ch->mu);
                if (ch->head != NULL || ch->closed) {
                    ready_idx = i;
                    pthread_mutex_unlock(&ch->mu);
                    break;
                }
                pthread_mutex_unlock(&ch->mu);
            }
        }
        if (ready_idx == -1) {
            usleep(1000); // 1ms
            loops++;
        }
    }
    if (ready_idx == -1) return -1;

    Chan* ch = (Chan*)chs[ready_idx];
    if (is_send[ready_idx]) {
        Skink_chan_send(ch, vals[ready_idx]);
    } else {
        void* data = Skink_chan_recv(ch);
        if (out_val) *out_val = data;
    }
    return ready_idx;
}

// === Future implementation ===

typedef struct {
    void* value;
    int ready;
    pthread_mutex_t mu;
    pthread_cond_t cv;
} Future;

void* Skink_future_make(void) {
    Future* f = calloc(1, sizeof(Future));
    pthread_mutex_init(&f->mu, NULL);
    pthread_cond_init(&f->cv, NULL);
    return f;
}

void Skink_future_set(void* f_ptr, void* val) {
    Future* f = (Future*)f_ptr;
    pthread_mutex_lock(&f->mu);
    f->value = val;
    f->ready = 1;
    pthread_cond_broadcast(&f->cv);
    pthread_mutex_unlock(&f->mu);
}

void* Skink_future_get(void* f_ptr) {
    Future* f = (Future*)f_ptr;
    pthread_mutex_lock(&f->mu);
    while (!f->ready) {
        pthread_cond_wait(&f->cv, &f->mu);
    }
    void* val = f->value;
    pthread_mutex_unlock(&f->mu);
    return val;
}

// === Spawn ===

typedef struct {
    void* (*fn)(void*);
    void* arg;
    void* future;
} SpawnArgs;

static void* spawn_trampoline(void* arg) {
    SpawnArgs* sa = (SpawnArgs*)arg;
    void* result = sa->fn(sa->arg);
    if (sa->future) {
        Skink_future_set(sa->future, result);
    }
    free(sa);
    return result;
}

void Skink_spawn(void* fn, void* arg, void* future) {
    pthread_t tid;
    SpawnArgs* sa = malloc(sizeof(SpawnArgs));
    sa->fn = (void* (*)(void*))fn;
    sa->arg = arg;
    sa->future = future;
    pthread_create(&tid, NULL, spawn_trampoline, sa);
    pthread_detach(tid);
}

// Memory helpers
void Skink_free(void* p) {
    free(p);
}
