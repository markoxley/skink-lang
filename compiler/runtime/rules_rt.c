#include <pthread.h>
#include <stdlib.h>
#include <unistd.h>

typedef void (*SkinkRuleFn)(void *);

typedef struct SkinkRuleEntry {
  void *self;
  SkinkRuleFn eval;
  SkinkRuleFn start;
  SkinkRuleFn stop;
  int priority;
} SkinkRuleEntry;

typedef struct SkinkRuleset {
  int running;
  SkinkRuleEntry *rules;
  int count;
  int capacity;
  pthread_mutex_t mu;
} SkinkRuleset;

SkinkRuleset *Skink_ruleset_create() {
  SkinkRuleset *rs = calloc(1, sizeof(SkinkRuleset));
  rs->capacity = 8;
  rs->rules = malloc(sizeof(SkinkRuleEntry) * rs->capacity);
  pthread_mutex_init(&rs->mu, NULL);
  return rs;
}

void Skink_ruleset_add_rule(SkinkRuleset *rs, void *self, SkinkRuleFn eval,
                            SkinkRuleFn start, SkinkRuleFn stop, int priority) {
  pthread_mutex_lock(&rs->mu);
  if (rs->count >= rs->capacity) {
    rs->capacity *= 2;
    rs->rules = realloc(rs->rules, sizeof(SkinkRuleEntry) * rs->capacity);
  }
  SkinkRuleEntry *r = &rs->rules[rs->count];
  r->self = self;
  r->eval = eval;
  r->start = start;
  r->stop = stop;
  r->priority = priority;
  rs->count++;
  if (rs->running && start) {
    start(self);
  }
  pthread_mutex_unlock(&rs->mu);
}

void Skink_ruleset_start(SkinkRuleset *rs) {
  pthread_mutex_lock(&rs->mu);
  rs->running = 1;
  for (int i = 0; i < rs->count; i++) {
    if (rs->rules[i].start) {
      rs->rules[i].start(rs->rules[i].self);
    }
  }
  pthread_mutex_unlock(&rs->mu);
}

void Skink_ruleset_stop(SkinkRuleset *rs) {
  pthread_mutex_lock(&rs->mu);
  rs->running = 0;
  for (int i = 0; i < rs->count; i++) {
    if (rs->rules[i].stop) {
      rs->rules[i].stop(rs->rules[i].self);
    }
  }
  pthread_mutex_unlock(&rs->mu);
}

void Skink_ruleset_reset(SkinkRuleset *rs) {
  pthread_mutex_lock(&rs->mu);
  rs->running = 0;
  rs->count = 0;
  pthread_mutex_unlock(&rs->mu);
}

int Skink_ruleset_is_running(SkinkRuleset *rs) {
  pthread_mutex_lock(&rs->mu);
  int r = rs->running;
  pthread_mutex_unlock(&rs->mu);
  return r;
}

void Skink_ruleset_evaluate(SkinkRuleset *rs) {
  for (int i = 0; i < rs->count; i++) {
    rs->rules[i].eval(rs->rules[i].self);
  }
}

void *Skink_ruleset_loop(void *arg) {
  SkinkRuleset *rs = (SkinkRuleset *)arg;
  while (1) {
    if (!Skink_ruleset_is_running(rs))
      break;
    Skink_ruleset_evaluate(rs);
    usleep(1000);
  }
  return NULL;
}
