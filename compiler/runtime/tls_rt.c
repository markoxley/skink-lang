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
// TLS runtime for HTTPS support using OpenSSL.

#include <openssl/ssl.h>
#include <openssl/err.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// Skink_tls_ctx_create creates an SSL_CTX configured for server TLS.
void* Skink_tls_ctx_create() {
    const SSL_METHOD* method = TLS_server_method();
    SSL_CTX* ctx = SSL_CTX_new(method);
    if (!ctx) {
        return NULL;
    }
    return ctx;
}

// Skink_tls_ctx_use_certificate_file loads the certificate PEM file.
int Skink_tls_ctx_use_certificate_file(void* ctx, const char* certPath) {
    if (!ctx || !certPath) return -1;
    if (SSL_CTX_use_certificate_file((SSL_CTX*)ctx, certPath, SSL_FILETYPE_PEM) <= 0) {
        return -1;
    }
    return 0;
}

// Skink_tls_ctx_use_private_key_file loads the private key PEM file.
int Skink_tls_ctx_use_private_key_file(void* ctx, const char* keyPath) {
    if (!ctx || !keyPath) return -1;
    if (SSL_CTX_use_PrivateKey_file((SSL_CTX*)ctx, keyPath, SSL_FILETYPE_PEM) <= 0) {
        return -1;
    }
    return 0;
}

// Skink_tls_ctx_check_private_key verifies that the private key matches the cert.
int Skink_tls_ctx_check_private_key(void* ctx) {
    if (!ctx) return -1;
    if (SSL_CTX_check_private_key((SSL_CTX*)ctx) != 1) {
        return -1;
    }
    return 0;
}

// Skink_tls_accept wraps an accepted socket fd in an SSL session.
void* Skink_tls_accept(void* ctx, int fd) {
    if (!ctx) return NULL;
    SSL* ssl = SSL_new((SSL_CTX*)ctx);
    if (!ssl) return NULL;
    SSL_set_fd(ssl, fd);
    if (SSL_accept(ssl) <= 0) {
        SSL_free(ssl);
        return NULL;
    }
    return ssl;
}

// Skink_tls_read reads data from an SSL connection.
int Skink_tls_read(void* ssl, char* buf, int len) {
    if (!ssl || !buf || len <= 0) return -1;
    return SSL_read((SSL*)ssl, buf, len);
}

// Skink_tls_write writes data to an SSL connection.
int Skink_tls_write(void* ssl, const char* buf, int len) {
    if (!ssl || !buf || len <= 0) return -1;
    return SSL_write((SSL*)ssl, buf, len);
}

// Skink_tls_close frees the SSL session and shuts down the connection.
void Skink_tls_close(void* ssl) {
    if (!ssl) return;
    SSL_shutdown((SSL*)ssl);
    SSL_free((SSL*)ssl);
}

// Skink_tls_ctx_free frees the SSL_CTX.
void Skink_tls_ctx_free(void* ctx) {
    if (!ctx) return;
    SSL_CTX_free((SSL_CTX*)ctx);
}
