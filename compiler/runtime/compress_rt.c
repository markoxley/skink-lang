/*
 * Skink compression runtime bindings.
 *
 * This file provides thin C wrappers around the zlib library so that the
 * Skink compiler can emit calls to compression, decompression, and CRC32
 * routines without depending directly on zlib types in generated code.
 *
 * All functions return signed int values suitable for Skink's i32 type.
 * Negative values conventionally signal errors, while non-negative values
 * represent sizes or checksum results.
 */

#include <zlib.h>

/*
 * Skink_compress_bound
 *
 * Computes an upper bound on the number of bytes required to compress
 * `srcLen` bytes of source data. Callers can use this to allocate a
 * destination buffer large enough for Skink_compress().
 *
 * Parameters:
 *   srcLen - the number of input bytes to be compressed.
 *
 * Returns:
 *   The maximum number of bytes that compression may produce.
 */
int Skink_compress_bound(int srcLen) {
  return (int)compressBound(srcLen);
}

/*
 * Skink_compress
 *
 * Compresses `srcLen` bytes from `src` into `dest` using the default
 * zlib compression level. The destination buffer must be at least
 * Skink_compress_bound(srcLen) bytes long to guarantee success.
 *
 * Parameters:
 *   src     - pointer to the uncompressed input data.
 *   srcLen  - length of the input data in bytes.
 *   dest    - pointer to the output buffer that will hold compressed data.
 *   destCap - capacity of the output buffer in bytes.
 *
 * Returns:
 *   The number of bytes written to `dest` on success, or -1 on failure.
 */
int Skink_compress(const char *src, int srcLen, char *dest, int destCap) {
  /* zlib expects the destination length as an in/out parameter. */
  uLongf destLen = destCap;

  int rc = compress((Bytef *)dest, &destLen, (const Bytef *)src, srcLen);
  if (rc != Z_OK)
    return -1;

  return (int)destLen;
}

/*
 * Skink_uncompress
 *
 * Decompresses `srcLen` bytes of zlib-compressed data from `src` into
 * `dest`. The caller must provide a destination buffer large enough to
 * hold the uncompressed output.
 *
 * Parameters:
 *   src     - pointer to the compressed input data.
 *   srcLen  - length of the compressed input in bytes.
 *   dest    - pointer to the output buffer for uncompressed data.
 *   destCap - capacity of the output buffer in bytes.
 *
 * Returns:
 *   The number of bytes written to `dest` on success, or -1 on failure.
 */
int Skink_uncompress(const char *src, int srcLen, char *dest, int destCap) {
  /* zlib expects the destination length as an in/out parameter. */
  uLongf destLen = destCap;

  int rc = uncompress((Bytef *)dest, &destLen, (const Bytef *)src, srcLen);
  if (rc != Z_OK)
    return -1;

  return (int)destLen;
}

/*
 * Skink_crc32
 *
 * Computes the CRC-32 checksum of `len` bytes starting at `data`.
 * The checksum is initialized to zero, so this function returns the
 * standalone CRC-32 value of the provided buffer.
 *
 * Parameters:
 *   data - pointer to the input data.
 *   len  - length of the input data in bytes.
 *
 * Returns:
 *   The 32-bit CRC checksum as a signed integer.
 */
int Skink_crc32(const char *data, int len) {
  return (int)crc32(0, (const Bytef *)data, len);
}
