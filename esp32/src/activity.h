// Portable mirror of internal/audio/activity.go. Checked against identical PCM.
#ifndef SONIC_ACTIVITY_H
#define SONIC_ACTIVITY_H
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <algorithm>

namespace sonic {
class Activity {
    double alpha_[3] = {}, low_[3] = {};
    double dc_ = 0, dcAlpha_, smoothing_, ratio_;
    double energy_[6] = {}, reference_[6] = {};
    bool initialized_ = false;
    uint64_t stable_ = 0, settle_;
public:
    Activity(uint32_t rate, double sensitivityDB, double settleSeconds)
        : dcAlpha_(1-std::exp(-2*3.14159265358979323846*20/rate)),
          smoothing_(rate*0.1), ratio_(std::pow(10, sensitivityDB/10)),
          settle_(static_cast<uint64_t>(settleSeconds*rate)) {
        const double fractions[] = {1.0/64, 1.0/16, 3.0/16};
        for (int i=0; i<3; ++i) alpha_[i] = 1-std::exp(-2*3.14159265358979323846*fractions[i]);
    }
    bool active(const int16_t* samples, size_t count) {
        if (!count) return true;
        double energy[6] = {};
        for (size_t n=0; n<count; ++n) {
            double x = samples[n];
            dc_ += dcAlpha_*(x-dc_);
            x -= dc_;
            for (int i=0; i<3; ++i) low_[i] += alpha_[i]*(x-low_[i]);
            const double bands[] = {x, low_[0], low_[1]-low_[0], low_[2]-low_[1], x-low_[2]};
            for (int i=0; i<5; ++i) energy[i] += bands[i]*bands[i];
            energy[5] = std::max(energy[5], x*x);
        }
        const double weight = 1-std::exp(-static_cast<double>(count)/smoothing_);
        bool changed = !initialized_;
        for (int i=0; i<6; ++i) {
            if (i<5) energy[i] /= count;
            energy[i] = std::max(64.0, energy[i]);
            energy_[i] += weight*(energy[i]-energy_[i]);
            const double value = std::max(64.0, energy_[i]);
            if (value>reference_[i]*ratio_ || value*ratio_<reference_[i]) changed = true;
            if (i==5 && energy[i]>std::max(64.0, reference_[i])*ratio_*ratio_) changed = true;
        }
        if (changed) {
            for (int i=0; i<6; ++i) reference_[i] = std::max(64.0, energy_[i]);
            stable_ = 0;
            initialized_ = true;
        } else if (stable_<settle_) stable_ += count;
        return stable_<settle_;
    }
};
}
#endif
