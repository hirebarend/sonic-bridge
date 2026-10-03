#include "activity.h"
#include "wire.h"
#include <cstdio>
#include <cstdint>
int main() {
    sonic::Activity detector(16000, 3, 30);
    uint8_t bytes[640];
    while (std::fread(bytes, 1, sizeof(bytes), stdin)==sizeof(bytes)) {
        int16_t samples[320];
        for (int i=0; i<320; ++i) samples[i]=static_cast<int16_t>(bytes[2*i] | (bytes[2*i+1]<<8));
        std::putchar(detector.active(samples, 320) ? 1 : 0);
    }
    return std::ferror(stdin) ? 1 : 0;
}
