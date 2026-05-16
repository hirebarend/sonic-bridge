// sonic-bridge console: connects to the server's destination port and plays
// the received raw PCM stream through PortAudio.
//
// Wire format (must match server and ESP32):
//   16 kHz, 16-bit signed LE PCM, mono, raw stream (no framing).

#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <csignal>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include <portaudio.h>

namespace {

constexpr int kSampleRate = 8000;
constexpr int kFramesPerBuffer = 1024;            // 128 ms per audio callback
constexpr size_t kRingSamples = kSampleRate;      // ~1 s ring
constexpr size_t kPrefillSamples = kSampleRate / 6; // ~167 ms before playback starts
constexpr size_t kRecvBufBytes = 8 * 1024;        // 4096 samples per recv() max

std::atomic<bool> g_stop{false};

void handleSignal(int) { g_stop.store(true, std::memory_order_relaxed); }

// Single-producer, single-consumer ring buffer of int16_t.
// Producer = network reader thread. Consumer = PortAudio callback.
//
// Storage is a power-of-two-free vector of `capacity` slots. We track head/tail
// as monotonically increasing 64-bit counters; the indexable position is
// `counter % capacity`. fill = head - tail.
class SpscRing {
public:
    explicit SpscRing(size_t capacity)
        : data_(capacity), capacity_(capacity) {}

    size_t capacity() const { return capacity_; }

    size_t fill() const {
        size_t head = head_.load(std::memory_order_acquire);
        size_t tail = tail_.load(std::memory_order_acquire);
        return head - tail;
    }

    // Returns number of samples actually written. Two memcpys around the wrap.
    size_t write(const int16_t* src, size_t n) {
        size_t head = head_.load(std::memory_order_relaxed);
        size_t tail = tail_.load(std::memory_order_acquire);
        size_t free_slots = capacity_ - (head - tail);
        size_t to_write = n < free_slots ? n : free_slots;
        if (to_write == 0) return 0;

        size_t head_idx = head % capacity_;
        size_t first = std::min(to_write, capacity_ - head_idx);
        std::memcpy(&data_[head_idx], src, first * sizeof(int16_t));
        if (to_write > first) {
            std::memcpy(&data_[0], src + first, (to_write - first) * sizeof(int16_t));
        }
        head_.store(head + to_write, std::memory_order_release);
        cv_.notify_one();
        return to_write;
    }

    // Reads up to n samples into dst. Returns samples actually read.
    size_t read(int16_t* dst, size_t n) {
        size_t tail = tail_.load(std::memory_order_relaxed);
        size_t head = head_.load(std::memory_order_acquire);
        size_t available = head - tail;
        size_t to_read = n < available ? n : available;
        if (to_read == 0) return 0;

        size_t tail_idx = tail % capacity_;
        size_t first = std::min(to_read, capacity_ - tail_idx);
        std::memcpy(dst, &data_[tail_idx], first * sizeof(int16_t));
        if (to_read > first) {
            std::memcpy(dst + first, &data_[0], (to_read - first) * sizeof(int16_t));
        }
        tail_.store(tail + to_read, std::memory_order_release);
        return to_read;
    }

    // Block until fill >= want or g_stop is set. Returns true if filled.
    bool wait_for_fill(size_t want) {
        std::unique_lock<std::mutex> lk(mu_);
        cv_.wait(lk, [&] {
            return g_stop.load(std::memory_order_relaxed) || fill() >= want;
        });
        return !g_stop.load(std::memory_order_relaxed);
    }

private:
    std::vector<int16_t> data_;
    size_t capacity_;
    std::atomic<size_t> head_{0};
    std::atomic<size_t> tail_{0};
    std::mutex mu_;
    std::condition_variable cv_;
};

struct CallbackCtx {
    SpscRing* ring;
    std::atomic<uint64_t> underruns{0};
    std::atomic<bool> started{false};
};

int paCallback(const void* /*in*/, void* out,
               unsigned long frames,
               const PaStreamCallbackTimeInfo* /*ti*/,
               PaStreamCallbackFlags /*flags*/,
               void* userData) {
    auto* ctx = static_cast<CallbackCtx*>(userData);
    auto* dst = static_cast<int16_t*>(out);
    if (!ctx->started.load(std::memory_order_acquire)) {
        // Pre-fill not complete; emit silence rather than starving the device.
        std::memset(dst, 0, frames * sizeof(int16_t));
        return paContinue;
    }
    size_t got = ctx->ring->read(dst, frames);
    if (got < frames) {
        std::memset(dst + got, 0, (frames - got) * sizeof(int16_t));
        ctx->underruns.fetch_add(1, std::memory_order_relaxed);
    }
    return paContinue;
}

int connectTo(const std::string& host, int port) {
    addrinfo hints{};
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    addrinfo* res = nullptr;
    std::string port_str = std::to_string(port);
    int rc = ::getaddrinfo(host.c_str(), port_str.c_str(), &hints, &res);
    if (rc != 0) {
        std::fprintf(stderr, "getaddrinfo(%s:%d): %s\n", host.c_str(), port, gai_strerror(rc));
        return -1;
    }
    int fd = -1;
    for (addrinfo* ai = res; ai != nullptr; ai = ai->ai_next) {
        fd = ::socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
        if (fd < 0) continue;
        if (::connect(fd, ai->ai_addr, ai->ai_addrlen) == 0) break;
        ::close(fd);
        fd = -1;
    }
    ::freeaddrinfo(res);
    if (fd < 0) {
        std::fprintf(stderr, "connect(%s:%d) failed: %s\n", host.c_str(), port, std::strerror(errno));
        return -1;
    }

    int one = 1;
    ::setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));
    int rcvbuf = 64 * 1024;
    ::setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
    return fd;
}

void readerLoop(int fd, SpscRing& ring) {
    std::vector<uint8_t> buf(kRecvBufBytes);
    while (!g_stop.load(std::memory_order_relaxed)) {
        ssize_t n = ::recv(fd, buf.data(), buf.size(), 0);
        if (n == 0) {
            std::fprintf(stderr, "server closed connection\n");
            break;
        }
        if (n < 0) {
            if (errno == EINTR) continue;
            std::fprintf(stderr, "recv error: %s\n", std::strerror(errno));
            break;
        }
        size_t samples = static_cast<size_t>(n) / sizeof(int16_t);
        const int16_t* src = reinterpret_cast<const int16_t*>(buf.data());
        size_t written = 0;
        while (written < samples && !g_stop.load(std::memory_order_relaxed)) {
            size_t w = ring.write(src + written, samples - written);
            written += w;
            if (w == 0) {
                // Ring is full. The audio callback is the only drain; back off
                // a hair to let it catch up. This also throttles TCP read pace
                // to the audio device clock.
                std::this_thread::sleep_for(std::chrono::milliseconds(2));
            }
        }
    }
    g_stop.store(true, std::memory_order_relaxed);
}

void usage(const char* argv0) {
    std::fprintf(stderr,
                 "Usage: %s [--host HOST] [--port PORT]\n"
                 "  --host  server hostname or IP (default 127.0.0.1)\n"
                 "  --port  TCP port            (default 9001; use 9002 for test tone)\n",
                 argv0);
}

} // namespace

int main(int argc, char** argv) {
    std::string host = "127.0.0.1";
    int port = 9001;

    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--host" && i + 1 < argc) {
            host = argv[++i];
        } else if (a == "--port" && i + 1 < argc) {
            port = std::atoi(argv[++i]);
        } else if (a == "-h" || a == "--help") {
            usage(argv[0]);
            return 0;
        } else {
            std::fprintf(stderr, "unknown arg: %s\n", a.c_str());
            usage(argv[0]);
            return 2;
        }
    }

    std::signal(SIGINT, handleSignal);
    std::signal(SIGTERM, handleSignal);
    std::signal(SIGPIPE, SIG_IGN);

    int fd = connectTo(host, port);
    if (fd < 0) return 1;
    std::fprintf(stderr, "connected to %s:%d\n", host.c_str(), port);

    SpscRing ring(kRingSamples);
    CallbackCtx ctx{&ring};

    PaError err = Pa_Initialize();
    if (err != paNoError) {
        std::fprintf(stderr, "Pa_Initialize: %s\n", Pa_GetErrorText(err));
        ::close(fd);
        return 1;
    }

    PaDeviceIndex outDev = Pa_GetDefaultOutputDevice();
    if (outDev == paNoDevice) {
        std::fprintf(stderr, "no default output device\n");
        Pa_Terminate();
        ::close(fd);
        return 1;
    }
    const PaDeviceInfo* devInfo = Pa_GetDeviceInfo(outDev);

    // Pin a host buffer size and ask for ~30 ms of device-side latency. We do
    // our own jitter buffering in the ring, so we don't need PortAudio to add
    // an extra cushion of its own.
    PaStreamParameters outParams{};
    outParams.device = outDev;
    outParams.channelCount = 1;
    outParams.sampleFormat = paInt16;
    outParams.suggestedLatency = devInfo ? std::max(devInfo->defaultLowOutputLatency, 0.03) : 0.03;
    outParams.hostApiSpecificStreamInfo = nullptr;

    PaStream* stream = nullptr;
    err = Pa_OpenStream(&stream,
                        nullptr,
                        &outParams,
                        static_cast<double>(kSampleRate),
                        kFramesPerBuffer,
                        paNoFlag,
                        &paCallback,
                        &ctx);
    if (err != paNoError) {
        std::fprintf(stderr, "Pa_OpenStream: %s\n", Pa_GetErrorText(err));
        Pa_Terminate();
        ::close(fd);
        return 1;
    }

    err = Pa_StartStream(stream);
    if (err != paNoError) {
        std::fprintf(stderr, "Pa_StartStream: %s\n", Pa_GetErrorText(err));
        Pa_CloseStream(stream);
        Pa_Terminate();
        ::close(fd);
        return 1;
    }

    std::thread reader(readerLoop, fd, std::ref(ring));

    // Wait for the ring to pre-fill before unmuting the callback. Until then
    // the callback emits silence (see paCallback). This avoids the guaranteed
    // initial underrun and gives us ~150 ms of jitter cushion to live at.
    if (ring.wait_for_fill(kPrefillSamples)) {
        ctx.started.store(true, std::memory_order_release);
        std::fprintf(stderr, "pre-fill complete (%zu samples, ~%zu ms); playback starting\n",
                     kPrefillSamples, (kPrefillSamples * 1000) / kSampleRate);
    }

    while (!g_stop.load(std::memory_order_relaxed)) {
        std::this_thread::sleep_for(std::chrono::milliseconds(200));
    }

    ::shutdown(fd, SHUT_RDWR);
    if (reader.joinable()) reader.join();

    Pa_StopStream(stream);
    Pa_CloseStream(stream);
    Pa_Terminate();
    ::close(fd);

    uint64_t under = ctx.underruns.load();
    std::fprintf(stderr, "shutdown clean (underrun callbacks: %llu)\n",
                 static_cast<unsigned long long>(under));
    return 0;
}
