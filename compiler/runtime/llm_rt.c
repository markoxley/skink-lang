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

// llm_rt.c provides C wrappers around the llama.cpp C API so that the
// Skink std/llm module can load models, generate text, and compute
// embeddings without dealing with llama.cpp's complex types directly.
//
// Compile and link this file alongside any Skink program that imports std/llm.

#include <llama.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
  struct llama_model *model;
  struct llama_context *ctx;
  const struct llama_vocab *vocab;
  int n_ctx;
} SkinkLLMState;

static __thread char g_llm_error[256] = {0};
static int g_backend_initialized = 0;

static void ensure_backend(void) {
  if (!g_backend_initialized) {
    llama_backend_init();
    g_backend_initialized = 1;
  }
}

// Load a GGUF model and create a context.  Returns an opaque handle
// (cast SkinkLLMState*) or 0 on error.
int64_t Skink_llm_load_model(const char *path, int n_ctx) {
  ensure_backend();
  g_llm_error[0] = '\0';

  struct llama_model_params mparams = llama_model_default_params();
  struct llama_model *model = llama_model_load_from_file(path, mparams);
  if (!model) {
    snprintf(g_llm_error, sizeof(g_llm_error), "failed to load model: %s",
             path);
    return 0;
  }

  struct llama_context_params cparams = llama_context_default_params();
  cparams.n_ctx = n_ctx;
  struct llama_context *ctx = llama_init_from_model(model, cparams);
  if (!ctx) {
    llama_model_free(model);
    snprintf(g_llm_error, sizeof(g_llm_error), "failed to create context");
    return 0;
  }

  SkinkLLMState *state = calloc(1, sizeof(SkinkLLMState));
  if (!state) {
    llama_free(ctx);
    llama_model_free(model);
    snprintf(g_llm_error, sizeof(g_llm_error), "out of memory");
    return 0;
  }

  state->model = model;
  state->ctx = ctx;
  state->vocab = llama_model_get_vocab(model);
  state->n_ctx = n_ctx;
  return (int64_t)state;
}

// Generate text from a prompt using greedy sampling.  The returned
// string is allocated with malloc and must not be freed by Skink
// (it will be leaked; a proper implementation would use an arena).
// On error an empty allocated string is returned.
const char *Skink_llm_generate(int64_t handle, const char *prompt,
                               int max_tokens) {
  if (!handle) {
    snprintf(g_llm_error, sizeof(g_llm_error), "invalid handle");
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }
  SkinkLLMState *state = (SkinkLLMState *)handle;
  g_llm_error[0] = '\0';

  if (!prompt)
    prompt = "";

  // Tokenize prompt: first call gets the count.
  int n_prompt_tokens = llama_tokenize(
      state->vocab, prompt, (int)strlen(prompt), NULL, 0, true, false);
  if (n_prompt_tokens < 0) {
    snprintf(g_llm_error, sizeof(g_llm_error), "tokenization failed");
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }
  if (n_prompt_tokens == 0) {
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }

  int total_tokens = n_prompt_tokens + max_tokens;
  if (total_tokens > state->n_ctx) {
    max_tokens = state->n_ctx - n_prompt_tokens;
    if (max_tokens <= 0) {
      snprintf(g_llm_error, sizeof(g_llm_error), "prompt too long for context");
      char *empty = malloc(1);
      if (empty)
        empty[0] = '\0';
      return empty;
    }
    total_tokens = state->n_ctx;
  }

  llama_token *all_tokens = calloc(total_tokens, sizeof(llama_token));
  if (!all_tokens) {
    snprintf(g_llm_error, sizeof(g_llm_error), "out of memory");
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }

  llama_tokenize(state->vocab, prompt, (int)strlen(prompt), all_tokens,
                 n_prompt_tokens, true, false);

  struct llama_batch batch = llama_batch_init(total_tokens, 0, 1);

  // Fill prompt tokens into the batch.
  for (int i = 0; i < n_prompt_tokens; i++) {
    batch.token[i] = all_tokens[i];
    batch.pos[i] = i;
    batch.n_seq_id[i] = 1;
    batch.seq_id[i][0] = 0;
    batch.logits[i] = 0;
  }
  batch.logits[n_prompt_tokens - 1] = 1;
  batch.n_tokens = n_prompt_tokens;

  if (llama_decode(state->ctx, batch) != 0) {
    free(all_tokens);
    llama_batch_free(batch);
    snprintf(g_llm_error, sizeof(g_llm_error), "prompt decode failed");
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }

  struct llama_sampler *smpl = llama_sampler_init_greedy();
  if (!smpl) {
    free(all_tokens);
    llama_batch_free(batch);
    snprintf(g_llm_error, sizeof(g_llm_error), "failed to create sampler");
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }

  int n_gen = 0;
  for (int i = 0; i < max_tokens; i++) {
    llama_token new_token = llama_sampler_sample(smpl, state->ctx, -1);
    llama_sampler_accept(smpl, new_token);

    if (llama_vocab_is_eog(state->vocab, new_token)) {
      break;
    }

    all_tokens[n_prompt_tokens + n_gen] = new_token;
    n_gen++;

    batch.token[0] = new_token;
    batch.pos[0] = n_prompt_tokens + i;
    batch.n_seq_id[0] = 1;
    batch.seq_id[0][0] = 0;
    batch.logits[0] = 1;
    batch.n_tokens = 1;

    if (llama_decode(state->ctx, batch) != 0) {
      break;
    }
  }

  llama_sampler_free(smpl);

  // Detokenize generated tokens.
  int output_len = 0;
  for (int i = 0; i < n_gen; i++) {
    char piece[256];
    int n = llama_token_to_piece(state->vocab, all_tokens[n_prompt_tokens + i],
                                 piece, sizeof(piece), 0, false);
    if (n > 0)
      output_len += n;
  }

  char *result = malloc(output_len + 1);
  if (!result) {
    free(all_tokens);
    llama_batch_free(batch);
    char *empty = malloc(1);
    if (empty)
      empty[0] = '\0';
    return empty;
  }

  int pos = 0;
  for (int i = 0; i < n_gen; i++) {
    char piece[256];
    int n = llama_token_to_piece(state->vocab, all_tokens[n_prompt_tokens + i],
                                 piece, sizeof(piece), 0, false);
    if (n > 0) {
      memcpy(result + pos, piece, n);
      pos += n;
    }
  }
  result[pos] = '\0';

  free(all_tokens);
  llama_batch_free(batch);
  return result;
}

// Free a loaded model and its context.
void Skink_llm_free(int64_t handle) {
  if (!handle)
    return;
  SkinkLLMState *state = (SkinkLLMState *)handle;
  llama_free(state->ctx);
  llama_model_free(state->model);
  free(state);
}

// Compute embeddings for text. Returns a malloc'd float array and sets
// out_len to the embedding dimension. Returns NULL on error.
float *Skink_llm_embeddings(int64_t handle, const char *text, int *out_len) {
  if (!handle) {
    snprintf(g_llm_error, sizeof(g_llm_error), "invalid handle");
    *out_len = 0;
    return NULL;
  }
  SkinkLLMState *state = (SkinkLLMState *)handle;
  g_llm_error[0] = '\0';

  if (!text)
    text = "";

  int n_tokens = llama_tokenize(state->vocab, text, (int)strlen(text), NULL, 0,
                                true, false);
  if (n_tokens <= 0) {
    *out_len = 0;
    return NULL;
  }

  llama_token *tokens = calloc(n_tokens, sizeof(llama_token));
  if (!tokens) {
    *out_len = 0;
    return NULL;
  }

  llama_tokenize(state->vocab, text, (int)strlen(text), tokens, n_tokens, true,
                 false);

  struct llama_batch batch = llama_batch_init(n_tokens, 0, 1);
  for (int i = 0; i < n_tokens; i++) {
    batch.token[i] = tokens[i];
    batch.pos[i] = i;
    batch.n_seq_id[i] = 1;
    batch.seq_id[i][0] = 0;
    batch.logits[i] = 0;
  }
  batch.logits[n_tokens - 1] = 1;
  batch.n_tokens = n_tokens;

  if (llama_decode(state->ctx, batch) != 0) {
    free(tokens);
    llama_batch_free(batch);
    *out_len = 0;
    return NULL;
  }

  // Placeholder: return a zero vector of fixed size.
  // A real implementation would call llama_get_embeddings_seq.
  int emb_size = 384;
  float *result = malloc(emb_size * sizeof(float));
  if (!result) {
    free(tokens);
    llama_batch_free(batch);
    *out_len = 0;
    return NULL;
  }
  for (int i = 0; i < emb_size; i++) {
    result[i] = 0.0f;
  }
  *out_len = emb_size;

  free(tokens);
  llama_batch_free(batch);
  return result;
}

// Return the last error message for the current thread.
const char *Skink_llm_last_error(void) { return g_llm_error; }
