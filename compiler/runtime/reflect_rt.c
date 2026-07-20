#include <stdint.h>

int reflect_get_bool(int64_t addr, int offset) {
    return *((uint8_t*)(addr + offset)) != 0;
}

void reflect_set_bool(int64_t addr, int offset, int val) {
    *((uint8_t*)(addr + offset)) = val ? 1 : 0;
}

int reflect_get_int(int64_t addr, int offset) {
    return *(int*)(addr + offset);
}

void reflect_set_int(int64_t addr, int offset, int val) {
    *(int*)(addr + offset) = val;
}

int64_t reflect_get_int64(int64_t addr, int offset) {
    return *(int64_t*)(addr + offset);
}

void reflect_set_int64(int64_t addr, int offset, int64_t val) {
    *(int64_t*)(addr + offset) = val;
}

const char* reflect_get_string(int64_t addr, int offset) {
    return *(const char**)(addr + offset);
}

void reflect_set_string(int64_t addr, int offset, const char* val) {
    *(const char**)(addr + offset) = val;
}

double reflect_get_float(int64_t addr, int offset) {
    return *(double*)(addr + offset);
}

void reflect_set_float(int64_t addr, int offset, double val) {
    *(double*)(addr + offset) = val;
}

int reflect_hash(int64_t addr, const char* typeName) {
    int hash = 0;
    for (int i = 0; typeName[i]; i++) {
        hash = hash * 31 + typeName[i];
    }
    return hash;
}
