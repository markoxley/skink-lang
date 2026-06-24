#include <zlib.h>

int Skink_compress_bound(int srcLen) { return (int)compressBound(srcLen); }

int Skink_compress(const char *src, int srcLen, char *dest, int destCap) {
  uLongf destLen = destCap;
  int rc = compress((Bytef *)dest, &destLen, (const Bytef *)src, srcLen);
  if (rc != Z_OK)
    return -1;
  return (int)destLen;
}

int Skink_uncompress(const char *src, int srcLen, char *dest, int destCap) {
  uLongf destLen = destCap;
  int rc = uncompress((Bytef *)dest, &destLen, (const Bytef *)src, srcLen);
  if (rc != Z_OK)
    return -1;
  return (int)destLen;
}

int Skink_crc32(const char *data, int len) {
  return (int)crc32(0, (const Bytef *)data, len);
}
