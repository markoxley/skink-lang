#include <math.h>
#include <stdlib.h>
#include <string.h>
#include <omp.h>
#ifdef __AVX2__
#include <immintrin.h>
#endif

typedef struct {
  int rows;
  int cols;
  double *data;
} Tensor;

void *Skink_tensor_ones(int rows, int cols) {
  Tensor *t = malloc(sizeof(Tensor));
  t->rows = rows;
  t->cols = cols;
  t->data = malloc(rows * cols * sizeof(double));
  for (int i = 0; i < rows * cols; i++) {
    t->data[i] = 1.0;
  }
  return t;
}

void *Skink_tensor_zeros(int rows, int cols) {
  Tensor *t = malloc(sizeof(Tensor));
  t->rows = rows;
  t->cols = cols;
  t->data = calloc(rows * cols, sizeof(double));
  return t;
}

__attribute__((always_inline)) static inline void Skink_tensor_matmul_data_row(const double *a_data, int k, const double *b_data, int n, double * __restrict c_data, int i) {
  int a_row = i * k;
  int c_row = i * n;
  for (int j = 0; j < n; j++) {
    c_data[c_row + j] = 0.0;
  }
  for (int k_idx = 0; k_idx < k; k_idx++) {
    double a_val = a_data[a_row + k_idx];
    int b_row = k_idx * n;
    for (int j = 0; j < n; j++) {
      c_data[c_row + j] += a_val * b_data[b_row + j];
    }
  }
}

void Skink_tensor_matmul_data(const double *a_data, int m, int k, const double *b_data, int n, double * __restrict c_data) {
  if (m * k * n > 100000000) {
    #pragma omp parallel for
    for (int i = 0; i < m; i++) {
      Skink_tensor_matmul_data_row(a_data, k, b_data, n, c_data, i);
    }
  } else {
    for (int i = 0; i < m; i++) {
      Skink_tensor_matmul_data_row(a_data, k, b_data, n, c_data, i);
    }
  }
}

void Skink_tensor_matmul4(int m, int k, int n, const double *a_data,
                          const double *b0, double *c0,
                          const double *b1, double *c1,
                          const double *b2, double *c2,
                          const double *b3, double *c3) {
  #pragma omp parallel sections
  {
    #pragma omp section
    Skink_tensor_matmul_data(a_data, m, k, b0, n, c0);
    #pragma omp section
    Skink_tensor_matmul_data(a_data, m, k, b1, n, c1);
    #pragma omp section
    Skink_tensor_matmul_data(a_data, m, k, b2, n, c2);
    #pragma omp section
    Skink_tensor_matmul_data(a_data, m, k, b3, n, c3);
  }
}

static inline double sigmoid(double x) {
  return 1.0 / (1.0 + exp(-x));
}

#define LSTM_IB 64
#define LSTM_KB 50
#define LSTM_NB 40

// Fallback scalar update for the tail of one c row.
static void lstm_matmul_row_scalar(const double * __restrict a, int a_stride,
                                   const double * __restrict b, double * __restrict c,
                                   int i, int k, int n) {
  const double *a_row = a + i * a_stride;
  double *c_row = c + i * n;
  for (int k2 = 0; k2 < k; k2++) {
    double a_val = a_row[k2];
    const double *b_row = b + k2 * n;
    for (int j2 = 0; j2 < n; j2++) {
      c_row[j2] += a_val * b_row[j2];
    }
  }
}

__attribute__((always_inline)) static inline void lstm_matmul_block(const double * __restrict a, int a_stride,
                              const double * __restrict b, double * __restrict c,
                              int i0, int i1, int k, int n) {
  if (n % LSTM_NB != 0 || k % LSTM_KB != 0) {
    for (int i = i0; i < i1; i++) {
      for (int j = 0; j < n; j++) c[i * n + j] = 0.0;
      lstm_matmul_row_scalar(a, a_stride, b, c, i, k, n);
    }
    return;
  }

  for (int i = i0; i < i1; i++) {
    for (int j = 0; j < n; j++) c[i * n + j] = 0.0;
  }

  double bb[LSTM_KB * LSTM_NB] __attribute__((aligned(32)));

  for (int k0 = 0; k0 < k; k0 += LSTM_KB) {
    for (int j0 = 0; j0 < n; j0 += LSTM_NB) {
      // Pack a b block into contiguous L1 storage.
      for (int k2 = 0; k2 < LSTM_KB; k2++) {
        const double *b_row = b + (k0 + k2) * n + j0;
        double *bb_row = bb + k2 * LSTM_NB;
        _mm256_store_pd(bb_row + 0, _mm256_loadu_pd(b_row + 0));
        _mm256_store_pd(bb_row + 4, _mm256_loadu_pd(b_row + 4));
        _mm256_store_pd(bb_row + 8, _mm256_loadu_pd(b_row + 8));
        _mm256_store_pd(bb_row + 12, _mm256_loadu_pd(b_row + 12));
        _mm256_store_pd(bb_row + 16, _mm256_loadu_pd(b_row + 16));
        _mm256_store_pd(bb_row + 20, _mm256_loadu_pd(b_row + 20));
        _mm256_store_pd(bb_row + 24, _mm256_loadu_pd(b_row + 24));
        _mm256_store_pd(bb_row + 28, _mm256_loadu_pd(b_row + 28));
        _mm256_store_pd(bb_row + 32, _mm256_loadu_pd(b_row + 32));
        _mm256_store_pd(bb_row + 36, _mm256_loadu_pd(b_row + 36));
      }

      // Compute c for this i-block and the packed b block.
      for (int i = i0; i < i1; i++) {
        const double *a_row = a + i * a_stride + k0;
        double *c_row = c + i * n + j0;

        __m256d c0 = _mm256_loadu_pd(c_row + 0);
        __m256d c1 = _mm256_loadu_pd(c_row + 4);
        __m256d c2 = _mm256_loadu_pd(c_row + 8);
        __m256d c3 = _mm256_loadu_pd(c_row + 12);
        __m256d c4 = _mm256_loadu_pd(c_row + 16);
        __m256d c5 = _mm256_loadu_pd(c_row + 20);
        __m256d c6 = _mm256_loadu_pd(c_row + 24);
        __m256d c7 = _mm256_loadu_pd(c_row + 28);
        __m256d c8 = _mm256_loadu_pd(c_row + 32);
        __m256d c9 = _mm256_loadu_pd(c_row + 36);

        for (int k2 = 0; k2 < LSTM_KB; k2++) {
          const double *bb_row = bb + k2 * LSTM_NB;
          __m256d av = _mm256_set1_pd(a_row[k2]);
          c0 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 0), c0);
          c1 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 4), c1);
          c2 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 8), c2);
          c3 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 12), c3);
          c4 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 16), c4);
          c5 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 20), c5);
          c6 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 24), c6);
          c7 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 28), c7);
          c8 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 32), c8);
          c9 = _mm256_fmadd_pd(av, _mm256_load_pd(bb_row + 36), c9);
        }

        _mm256_storeu_pd(c_row + 0, c0);
        _mm256_storeu_pd(c_row + 4, c1);
        _mm256_storeu_pd(c_row + 8, c2);
        _mm256_storeu_pd(c_row + 12, c3);
        _mm256_storeu_pd(c_row + 16, c4);
        _mm256_storeu_pd(c_row + 20, c5);
        _mm256_storeu_pd(c_row + 24, c6);
        _mm256_storeu_pd(c_row + 28, c7);
        _mm256_storeu_pd(c_row + 32, c8);
        _mm256_storeu_pd(c_row + 36, c9);
      }
    }
  }
}

void Skink_lstm_forward(const double *input_data, int m, int input_size,
                        const int *sizes, int n_sizes,
                        const double *weights, const double *bias,
                        double *h_data, int hidden_size) {
  int layers = n_sizes - 2;
  double *c = calloc(m * hidden_size, sizeof(double));
  double *gin[4];
  double *gh[4];
  for (int g = 0; g < 4; g++) {
    gin[g] = malloc(m * hidden_size * sizeof(double));
    gh[g] = malloc(m * hidden_size * sizeof(double));
  }

  const double *b = bias;
  const double *w = weights;
  const double *x = input_data;
  int x_cols = input_size;

  #pragma omp parallel
  {
    double *A[8];
    int K[8];
    const double *B[8];
    double *G[8];

    for (int l = 0; l < layers; l++) {
      int hidden = sizes[l + 1];
      int w_size = x_cols * hidden;
      int u_size = hidden * hidden;

      // Prepare per-gate inputs/outputs.
      for (int g = 0; g < 4; g++) {
        A[g] = (double *)x;
        K[g] = x_cols;
        B[g] = w + g * w_size;
        G[g] = gin[g];
      }
      for (int g = 0; g < 4; g++) {
        A[g + 4] = h_data;
        K[g + 4] = hidden;
        B[g + 4] = w + 4 * w_size + g * u_size;
        G[g + 4] = gh[g];
      }

      // Zero cell state for this layer.
      #pragma omp for
      for (int i = 0; i < m * hidden; i++) {
        c[i] = 0.0;
      }

      // Compute all 8 gate projections in parallel across gates and i-blocks.
      #pragma omp for collapse(2) schedule(static, 1)
      for (int g = 0; g < 8; g++) {
        for (int i0 = 0; i0 < m; i0 += LSTM_IB) {
          int i1 = i0 + LSTM_IB; if (i1 > m) i1 = m;
          lstm_matmul_block(A[g], K[g], B[g], G[g], i0, i1, K[g], hidden);
        }
      }

      // Combine gates and update cell/hidden state.
      #pragma omp for
      for (int i = 0; i < m * hidden; i++) {
        int col = i % hidden;
        double f = sigmoid(gin[0][i] + gh[0][i] + b[0 * hidden + col]);
        double ii = sigmoid(gin[1][i] + gh[1][i] + b[1 * hidden + col]);
        double gg = tanh(gin[2][i] + gh[2][i] + b[2 * hidden + col]);
        double o = sigmoid(gin[3][i] + gh[3][i] + b[3 * hidden + col]);
        c[i] = f * c[i] + ii * gg;
        h_data[i] = o * tanh(c[i]);
      }

      #pragma omp single
      {
        w += 4 * w_size + 4 * u_size;
        b += 4 * hidden;
        x = h_data;
        x_cols = hidden;
      }
      #pragma omp barrier
    }
  }

  free(c);
  for (int g = 0; g < 4; g++) {
    free(gin[g]);
    free(gh[g]);
  }
}

void *Skink_tensor_matmul(void *a_ptr, void *b_ptr) {
  Tensor *a = (Tensor *)a_ptr;
  Tensor *b = (Tensor *)b_ptr;
  int m = a->rows;
  int n = b->cols;
  int p = a->cols;
  Tensor *c = malloc(sizeof(Tensor));
  c->rows = m;
  c->cols = n;
  c->data = calloc(m * n, sizeof(double));
  Skink_tensor_matmul_data(a->data, m, p, b->data, n, c->data);
  return c;
}

double Skink_tensor_get(void *t_ptr, int row, int col) {
  Tensor *t = (Tensor *)t_ptr;
  return t->data[row * t->cols + col];
}

void Skink_tensor_free(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  free(t->data);
  free(t);
}

void *Skink_tensor_transpose(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  Tensor *res = malloc(sizeof(Tensor));
  res->rows = t->cols;
  res->cols = t->rows;
  res->data = malloc(t->rows * t->cols * sizeof(double));
  for (int i = 0; i < t->rows; i++) {
    for (int j = 0; j < t->cols; j++) {
      res->data[j * res->cols + i] = t->data[i * t->cols + j];
    }
  }
  return res;
}

double Skink_tensor_det(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  if (t->rows != t->cols)
    return 0.0;
  if (t->rows == 1)
    return t->data[0];
  if (t->rows == 2) {
    return t->data[0] * t->data[3] - t->data[1] * t->data[2];
  }
  if (t->rows == 3) {
    double a = t->data[0], b = t->data[1], c = t->data[2];
    double d = t->data[3], e = t->data[4], f = t->data[5];
    double g = t->data[6], h = t->data[7], i = t->data[8];
    return a * (e * i - f * h) - b * (d * i - f * g) + c * (d * h - e * g);
  }
  return 0.0;
}

void *Skink_tensor_inv(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  if (t->rows != t->cols)
    return NULL;
  double d = Skink_tensor_det(t);
  if (fabs(d) < 1e-9)
    return NULL;

  Tensor *res = malloc(sizeof(Tensor));
  res->rows = t->rows;
  res->cols = t->cols;
  res->data = malloc(t->rows * t->cols * sizeof(double));

  if (t->rows == 2) {
    res->data[0] = t->data[3] / d;
    res->data[1] = -t->data[1] / d;
    res->data[2] = -t->data[2] / d;
    res->data[3] = t->data[0] / d;
  } else if (t->rows == 3) {
    double a = t->data[0], b = t->data[1], c = t->data[2];
    double e = t->data[3], f = t->data[4], g = t->data[5];
    double h = t->data[6], i = t->data[7], j = t->data[8];
    res->data[0] = (f * j - g * i) / d;
    res->data[1] = (c * i - b * j) / d;
    res->data[2] = (b * g - c * f) / d;
    res->data[3] = (g * h - e * j) / d;
    res->data[4] = (a * j - c * h) / d;
    res->data[5] = (c * e - a * g) / d;
    res->data[6] = (e * i - f * h) / d;
    res->data[7] = (b * h - a * i) / d;
    res->data[8] = (a * f - b * e) / d;
  }
  return res;
}

double Skink_math_diff(double (*f)(double), double x) {
  double h = 1e-5;
  return (f(x + h) - f(x - h)) / (2.0 * h);
}

double Skink_math_integrate(double (*f)(double), double a, double b) {
  int n = 1000;
  double h = (b - a) / n;
  double sum = 0.5 * (f(a) + f(b));
  for (int i = 1; i < n; i++) {
    sum += f(a + i * h);
  }
  return sum * h;
}

void *Skink_tensor_gradient(double (*f)(double *), void *x_ptr) {
  Tensor *x = (Tensor *)x_ptr;
  Tensor *res = malloc(sizeof(Tensor));
  res->rows = x->rows;
  res->cols = x->cols;
  int n = x->rows * x->cols;
  res->data = malloc(n * sizeof(double));

  double *temp = malloc(n * sizeof(double));
  memcpy(temp, x->data, n * sizeof(double));

  double h = 1e-5;
  for (int i = 0; i < n; i++) {
    double old = temp[i];
    temp[i] = old + h;
    double f1 = f(temp);
    temp[i] = old - h;
    double f2 = f(temp);
    temp[i] = old;
    res->data[i] = (f1 - f2) / (2.0 * h);
  }
  free(temp);
  return res;
}

double Skink_tensor_dot(void *a_ptr, void *b_ptr) {
  Tensor *a = (Tensor *)a_ptr;
  Tensor *b = (Tensor *)b_ptr;
  int n = a->rows * a->cols;
  double sum = 0.0;
  for (int i = 0; i < n; i++) {
    sum += a->data[i] * b->data[i];
  }
  return sum;
}

void *Skink_tensor_cross(void *a_ptr, void *b_ptr) {
  Tensor *a = (Tensor *)a_ptr;
  Tensor *b = (Tensor *)b_ptr;
  Tensor *res = malloc(sizeof(Tensor));
  res->rows = 3;
  res->cols = 1;
  res->data = malloc(3 * sizeof(double));
  res->data[0] = a->data[1] * b->data[2] - a->data[2] * b->data[1];
  res->data[1] = a->data[2] * b->data[0] - a->data[0] * b->data[2];
  res->data[2] = a->data[0] * b->data[1] - a->data[1] * b->data[0];
  return res;
}

double Skink_tensor_norm(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  int n = t->rows * t->cols;
  double sum = 0.0;
  for (int i = 0; i < n; i++) {
    sum += t->data[i] * t->data[i];
  }
  return sqrt(sum);
}

void *Skink_tensor_eigenvalues(void *t_ptr) {
  Tensor *t = (Tensor *)t_ptr;
  Tensor *res = malloc(sizeof(Tensor));
  res->rows = 2;
  res->cols = 1;
  res->data = malloc(2 * sizeof(double));
  if (t->rows == 2 && t->cols == 2) {
    double a = t->data[0];
    double b = t->data[1];
    double c = t->data[2];
    double d = t->data[3];
    double trace = a + d;
    double det = a * d - b * c;
    double disc = sqrt(trace * trace - 4.0 * det);
    res->data[0] = (trace + disc) / 2.0;
    res->data[1] = (trace - disc) / 2.0;
  } else {
    res->data[0] = 0.0;
    res->data[1] = 0.0;
  }
  return res;
}
