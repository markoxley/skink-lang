#include <math.h>
#include <stdlib.h>
#include <string.h>

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
  for (int i = 0; i < m; i++) {
    for (int j = 0; j < n; j++) {
      double sum = 0.0;
      for (int k = 0; k < p; k++) {
        sum += a->data[i * p + k] * b->data[k * n + j];
      }
      c->data[i * n + j] = sum;
    }
  }
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
