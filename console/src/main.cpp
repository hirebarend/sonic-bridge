// sonic-bridge console: connects to the server's destination port and plays
// the received raw PCM stream through PortAudio.
//
// Wire format (must match server and ESP32):
//   16 kHz, 16-bit signed LE PCM, mono, raw stream (no framing).

#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <atomic>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <csignal>
#include <string>
#include <thread>
#include <vector>

#include <portaudio.h>

namespace {

constexpr int kSampleRate = 16000;
constexpr int kFramesPerBuffer = 1024;
// Ring buffer holds ~1 s of audio. Large enough to absorb jitter, small
// enough to keep latency bounded.
constexpr size_t kRingSamples = kSampleRate;

std::atomic<bool> g_stop{false};

void handleSignal(int) { g_stop.store(true, std::memory_order_relaxed); }

// Single-producer, single-consumer ring buffer of int16_t.
// Producer = network reader thread. Consumer = PortAudio callback.
class SpscRing {
public:
    explicit SpscRing(size_t capacity)
        : data_(capacity + 1), capacity_(capacity + 1) {}

    // Returns number of samples actually written.
    size_t write(const int16_t* src, size_t n) {
        size_t head = head_.load(std::memory_order_relaxed);
        size_t tail = tail_.load(std::memory_order_acquire);
        size_t written = 0;
        while (written < n) {
            size_t next = (head + 1) % capacity_;
            if (next == tail) break; // full
            data_[head] = src[written++];
            head = next;
        }
        head_.store(head, std::memory_order_release);
        return written;
    }

    // Reads up to n samples into dst. Returns samples actually read.
    size_t read(int16_t* dst, size_t n) {
        size_t tail = tail_.load(std::memory_order_relaxed);
        size_t head = head_.load(std::memory_order_acquire);
        size_t read_n = 0;
        while (read_n < n && tail != head) {
            dst[read_n++] = data_[tail];
            tail = (tail + 1) % capacity_;
        }
        tail_.store(tail, std::memory_order_release);
        return read_n;
    }

private:
    std::vector<int16_t> data_;
    size_t capacity_;
    std::atomic<size_t> head_{0};
    std::atomic<size_t> tail_{0};
};

struct CallbackCtx {
    SpscRing* ring;
    std::atomic<uint64_t> underruns{0};
};

int paCallback(const void* /*in*/, void* out,
               unsigned long frames,
               const PaStreamCallbackTimeInfo* /*ti*/,
               PaStreamCallbackFlags /*flags*/,
               void* userData) {
    auto* ctx = static_cast<CallbackCtx*>(userData);
    auto* dst = static_cast<int16_t*>(out);
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
    }
    return fd;
}

void readerLoop(int fd, SpscRing& ring) {
    constexpr size_t kBufSamples = 1024;
    int16_t buf[kBufSamples];
    while (!g_stop.load(std::memory_order_relaxed)) {
        ssize_t n = ::recv(fd, reinterpret_cast<char*>(buf), sizeof(buf), 0);
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
        size_t written = 0;
        while (written < samples && !g_stop.load(std::memory_order_relaxed)) {
            size_t w = ring.write(buf + written, samples - written);
            written += w;
            if (w == 0) {
                // Ring is full. Audio callback will catch up in a few ms.
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

    PaStream* stream = nullptr;
    err = Pa_OpenDefaultStream(&stream,
                               0, 1, paInt16,
                               static_cast<double>(kSampleRate),
                               kFramesPerBuffer,
                               &paCallback,
                               &ctx);
    if (err != paNoError) {
        std::fprintf(stderr, "Pa_OpenDefaultStream: %s\n", Pa_GetErrorText(err));
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
