// sonic-bridge ESP32-C3 firmware: an audio source.
//
// Capture analyzes environmental activity, retains 200 ms of pre-roll, and
// hands fixed frames/state to a bounded drop-oldest queue. Transmit sends SB02
// records, coalescing quiet states into five-second heartbeats. Capture never
// waits on network I/O.
//
// Configuration comes from build flags, set in secrets.ini. Copy
// secrets.ini.example and fill it in; the real file is not tracked by git.
//
// INMP441 wiring (mic pin -> ESP32-C3 GPIO):
//   VDD -> 3V3
//   GND -> GND
//   L/R -> GND          selects the left I2S slot
//   SCK -> SONIC_I2S_SCK    default GPIO 4
//   WS  -> SONIC_I2S_WS     default GPIO 5
//   SD  -> SONIC_I2S_SD     default GPIO 6
//
// Pin notes (ESP32-C3-DevKitM-1): GPIO 4/5/6 are safe general-purpose pins,
// and 7, 8 and 10 are also free. Avoid 11-17 (SPI flash), 18-19 (USB-JTAG on
// most modules), 20-21 (UART0 used by Serial) and the strapping pins 2/8/9.

#include <Arduino.h>
#include <atomic>
#include <WiFi.h>
#include <WiFiClient.h>
#include <lwip/sockets.h>

#include "driver/i2s_std.h"
#include "esp_err.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

#include "wire.h"
#include "activity.h"

#ifndef SONIC_WIFI_SSID
#define SONIC_WIFI_SSID "set-wifi-ssid-in-secrets-ini"
#endif
#ifndef SONIC_WIFI_PASS
#define SONIC_WIFI_PASS ""
#endif
#ifndef SONIC_RELAY_HOST
#define SONIC_RELAY_HOST "192.168.1.10"
#endif
#ifndef SONIC_RELAY_PORT
#define SONIC_RELAY_PORT 9000
#endif

// Wire format. The relay accepts whatever this declares, so these can change
// without touching the server. mu-law is the only codec the relay accepts: it
// halves the bandwidth of linear samples for a loss inaudible in speech.
#ifndef SONIC_SAMPLE_RATE
#define SONIC_SAMPLE_RATE 16000
#endif
#ifndef SONIC_FRAME_SAMPLES
#define SONIC_FRAME_SAMPLES 320
#endif

// Suppression tuning: lower dB means more sensitive to environmental changes.
#ifndef SONIC_SUPPRESSION
#define SONIC_SUPPRESSION 1
#endif
#ifndef SONIC_SENSITIVITY_DB
#define SONIC_SENSITIVITY_DB 3.0
#endif
#ifndef SONIC_SETTLE_SECONDS
#define SONIC_SETTLE_SECONDS 30
#endif

// Microphone and pinout.
#ifndef SONIC_INMP441_SHIFT
#define SONIC_INMP441_SHIFT 14
#endif
#ifndef SONIC_I2S_SCK
#define SONIC_I2S_SCK 4
#endif
#ifndef SONIC_I2S_WS
#define SONIC_I2S_WS 5
#endif
#ifndef SONIC_I2S_SD
#define SONIC_I2S_SD 6
#endif

// DMA: 8 descriptors of 512 frames is 4096 frames, which is 256 ms at 16 kHz.
#define SONIC_I2S_DMA_DESC_NUM 8
#define SONIC_I2S_DMA_FRAME_NUM 512

// TCP send buffer hint for lwIP. Larger trades RAM for jitter tolerance.
#define SONIC_TCP_SNDBUF_BYTES (16 * 1024)

// Pause before rebuilding a connection that actually dropped. It is not used
// for a single failed write.
#define SONIC_RECONNECT_MS 250

namespace {

constexpr uint32_t kSampleRate = SONIC_SAMPLE_RATE;
constexpr uint16_t kFrameSamples = SONIC_FRAME_SAMPLES;
constexpr size_t kFrameBytes = kFrameSamples;
constexpr int kInmpShift = SONIC_INMP441_SHIFT;

static_assert(kSampleRate >= 4000 && kSampleRate <= 48000, "unsupported sample rate");
static_assert(kFrameSamples > 0, "frame size must be positive");
static_assert(kInmpShift >= 0 && kInmpShift <= 31, "invalid microphone shift");
static_assert(SONIC_SENSITIVITY_DB >= 0.5 && SONIC_SENSITIVITY_DB <= 24, "invalid sensitivity");
static_assert(SONIC_SETTLE_SECONDS >= 1 && SONIC_SETTLE_SECONDS <= 600, "invalid settle period");
constexpr size_t kPreFrames = (kSampleRate/5 + kFrameSamples-1)/kFrameSamples;
constexpr size_t kQueueFrames = 16;
struct Frame {
    uint64_t position;
    bool quiet;
    uint8_t audio[kFrameBytes];
};
// Encoded pre-roll avoids a second PCM buffer on the microcontroller.
Frame g_preRoll[kPreFrames];
constexpr uint32_t kStatusIntervalMs = 5000;

i2s_chan_handle_t g_capture = nullptr;
QueueHandle_t g_frames = nullptr;
WiFiClient g_relay;

// Scratch buffers owned by the capture task alone.
int32_t g_slotBuffer[kFrameSamples];
int16_t g_sampleBuffer[kFrameSamples];


std::atomic<uint32_t> g_droppedFrames{0}, g_sentFrames{0}, g_audioBytes{0}, g_controlBytes{0}, g_quietFrames{0};
std::atomic<int> g_peakSample{0};

bool startCapture() {
    i2s_chan_config_t channelConfig = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    channelConfig.dma_desc_num = SONIC_I2S_DMA_DESC_NUM;
    channelConfig.dma_frame_num = SONIC_I2S_DMA_FRAME_NUM;

    if (i2s_new_channel(&channelConfig, nullptr, &g_capture) != ESP_OK) {
        Serial.println("i2s_new_channel failed");
        return false;
    }

    // The ESP-IDF helper macros are avoided here because their
    // designated-initialiser order does not match struct declaration order on
    // every RISC-V target, which is a hard error in C++.
    i2s_std_config_t standardConfig = {};

    standardConfig.clk_cfg.sample_rate_hz = kSampleRate;
    standardConfig.clk_cfg.clk_src = I2S_CLK_SRC_DEFAULT;
    standardConfig.clk_cfg.mclk_multiple = I2S_MCLK_MULTIPLE_256;

    standardConfig.slot_cfg.data_bit_width = I2S_DATA_BIT_WIDTH_32BIT;
    standardConfig.slot_cfg.slot_bit_width = I2S_SLOT_BIT_WIDTH_AUTO;
    standardConfig.slot_cfg.slot_mode = I2S_SLOT_MODE_MONO;
    standardConfig.slot_cfg.slot_mask = I2S_STD_SLOT_LEFT;
    standardConfig.slot_cfg.ws_width = I2S_DATA_BIT_WIDTH_32BIT;
    standardConfig.slot_cfg.ws_pol = false;
    standardConfig.slot_cfg.bit_shift = true;
    standardConfig.slot_cfg.left_align = true;
    standardConfig.slot_cfg.big_endian = false;
    standardConfig.slot_cfg.bit_order_lsb = false;

    standardConfig.gpio_cfg.mclk = I2S_GPIO_UNUSED;
    standardConfig.gpio_cfg.bclk = static_cast<gpio_num_t>(SONIC_I2S_SCK);
    standardConfig.gpio_cfg.ws = static_cast<gpio_num_t>(SONIC_I2S_WS);
    standardConfig.gpio_cfg.dout = I2S_GPIO_UNUSED;
    standardConfig.gpio_cfg.din = static_cast<gpio_num_t>(SONIC_I2S_SD);

    if (i2s_channel_init_std_mode(g_capture, &standardConfig) != ESP_OK) {
        Serial.println("i2s_channel_init_std_mode failed");
        return false;
    }

    if (i2s_channel_enable(g_capture) != ESP_OK) {
        Serial.println("i2s_channel_enable failed");
        return false;
    }

    return true;
}

// shiftSlotsToSamples converts the microphone's 24-bit samples, which arrive
// MSB-aligned in a 32-bit slot, to the linear 16-bit samples every codec
// works with. Tune the shift with SONIC_INMP441_SHIFT.
void shiftSlotsToSamples(const int32_t* slots, size_t sampleCount, int16_t* samples) {
    int16_t peak = 0;

    for (size_t i = 0; i < sampleCount; ++i) {
        int32_t value = slots[i] >> kInmpShift;

        if (value > INT16_MAX) {
            value = INT16_MAX;
        }

        if (value < INT16_MIN) {
            value = INT16_MIN;
        }

        samples[i] = static_cast<int16_t>(value);

        const int16_t magnitude = static_cast<int16_t>(value < 0 ? -(value + 1) : value);
        if (magnitude > peak) {
            peak = magnitude;
        }
    }

    g_peakSample = peak;
}

void discardBufferedFrames() {
    Frame discarded;
    while (xQueueReceive(g_frames, &discarded, 0) == pdTRUE) {}
}

bool writeAll(const uint8_t* payload, size_t payloadBytes) {
    size_t remaining = payloadBytes;

    while (remaining > 0) {
        const int written = g_relay.write(payload, remaining);

        if (written <= 0) {
            return false;
        }

        payload += written;
        remaining -= static_cast<size_t>(written);
    }

    return true;
}

bool ensureNetwork() {
    if (WiFi.status() == WL_CONNECTED) {
        return true;
    }

    Serial.printf("connecting to WiFi: %s\n", SONIC_WIFI_SSID);
    WiFi.mode(WIFI_STA);
    // Lower latency and fewer bursty stalls, at the cost of power.
    WiFi.setSleep(false);
    WiFi.begin(SONIC_WIFI_SSID, SONIC_WIFI_PASS);

    const uint32_t deadline = millis() + 20000;
    while (WiFi.status() != WL_CONNECTED && millis() < deadline) {
        delay(200);
    }

    if (WiFi.status() != WL_CONNECTED) {
        Serial.println("WiFi failed, will retry");
        return false;
    }

    Serial.printf("WiFi up, ip=%s rssi=%d\n", WiFi.localIP().toString().c_str(), WiFi.RSSI());

    return true;
}

// ensureRelay dials the relay and declares the wire format. Frames captured
// while the link was down are discarded here, so a reconnect resumes live
// instead of replaying a backlog.
bool ensureRelay() {
    if (g_relay.connected()) {
        return true;
    }

    Serial.printf("dialling relay %s:%d\n", SONIC_RELAY_HOST, SONIC_RELAY_PORT);

    if (!g_relay.connect(SONIC_RELAY_HOST, SONIC_RELAY_PORT)) {
        Serial.println("relay connect failed");
        return false;
    }

    g_relay.setNoDelay(true);

    const int socketHandle = g_relay.fd();
    if (socketHandle >= 0) {
        int sendBufferBytes = SONIC_TCP_SNDBUF_BYTES;
        ::setsockopt(socketHandle, SOL_SOCKET, SO_SNDBUF, &sendBufferBytes, sizeof(sendBufferBytes));
    }

    uint8_t header[sonic::kHeaderBytes];
    const size_t headerBytes = sonic::buildStreamHeader(header, sonic::kCodecMulaw, kSampleRate, kFrameSamples);
    header[3] = '2';

    if (!writeAll(header, headerBytes)) {
        Serial.println("could not send the stream header");
        g_relay.stop();
        return false;
    }

    g_controlBytes += headerBytes;
    discardBufferedFrames();
    Serial.printf("relay connected, streaming mulaw at %u Hz, %u samples per frame\n",
                  static_cast<unsigned>(kSampleRate),
                  static_cast<unsigned>(kFrameSamples));

    return true;
}

void captureTask(void*) {
    sonic::Activity detector(kSampleRate, SONIC_SENSITIVITY_DB, SONIC_SETTLE_SECONDS);
    size_t pending = 0, index = 0, filled = 0;
    uint64_t position = 0;
    for (;;) {
        size_t bytesRead = 0;
        const esp_err_t status = i2s_channel_read(g_capture,
            reinterpret_cast<uint8_t*>(g_slotBuffer)+pending,
            sizeof(g_slotBuffer)-pending, &bytesRead, portMAX_DELAY);
        if (status != ESP_OK) {
            pending = 0;
            Serial.printf("i2s read error: %d\n", status);
            vTaskDelay(pdMS_TO_TICKS(10));
            continue;
        }
        pending += bytesRead;
        if (pending < sizeof(g_slotBuffer)) continue;
        pending = 0;
        shiftSlotsToSamples(g_slotBuffer, kFrameSamples, g_sampleBuffer);
        const bool active = !SONIC_SUPPRESSION || detector.active(g_sampleBuffer, kFrameSamples);
        Frame current{};
        current.position = position;
        current.quiet = !active;
        sonic::encodeFrame(g_sampleBuffer, kFrameSamples, current.audio);
        position += kFrameSamples;
        Frame output = current;
        if (SONIC_SUPPRESSION) {
            output = g_preRoll[index];
            g_preRoll[index] = current;
            index = (index+1)%kPreFrames;
            if (filled < kPreFrames) { ++filled; continue; }
            output.quiet = output.quiet && !active;
        }
        if (output.quiet) ++g_quietFrames;
        if (xQueueSend(g_frames, &output, 0) != pdTRUE) {
            Frame discarded;
            if (xQueueReceive(g_frames, &discarded, 0) == pdTRUE) ++g_droppedFrames;
            xQueueSend(g_frames, &output, 0);
        }
    }
}

void transmitTask(void*) {
    bool quiet = false;
    uint32_t lastQuiet = 0;
    for (;;) {
        const bool wasConnected = g_relay.connected();
        if (!ensureNetwork() || !ensureRelay()) {
            quiet = false;
            vTaskDelay(pdMS_TO_TICKS(SONIC_RECONNECT_MS));
            continue;
        }
        if (!wasConnected) quiet = false;
        Frame frame;
        if (xQueueReceive(g_frames, &frame, pdMS_TO_TICKS(100)) != pdTRUE) continue;
        if (frame.quiet && quiet && static_cast<uint32_t>(millis()-lastQuiet) < 5000) continue;
        uint8_t record[sonic::kRecordHeaderBytes+kFrameBytes];
        sonic::buildRecordHeader(record, frame.quiet, frame.position);
        size_t bytes = sonic::kRecordHeaderBytes;
        if (!frame.quiet) { memcpy(record+bytes, frame.audio, kFrameBytes); bytes += kFrameBytes; }
        if (writeAll(record, bytes)) {
            quiet = frame.quiet;
            g_controlBytes += sonic::kRecordHeaderBytes;
            if (quiet) lastQuiet = millis();
            else { ++g_sentFrames; g_audioBytes += kFrameBytes; }
            continue;
        }
        Serial.println("relay write failed, reconnecting");
        quiet = false;
        g_relay.stop();
        discardBufferedFrames();
        vTaskDelay(pdMS_TO_TICKS(SONIC_RECONNECT_MS));
    }
}

}  // namespace

void setup() {
    Serial.begin(115200);
    delay(200);
    Serial.println("\nsonic-bridge esp32 source starting");

    if (!startCapture()) {
        Serial.println("FATAL: I2S setup failed, halting");
        while (true) {
            delay(1000);
        }
    }

    g_frames = xQueueCreate(kQueueFrames, sizeof(Frame));
    if (g_frames == nullptr) {
        Serial.println("FATAL: xQueueCreate failed, halting");
        while (true) {
            delay(1000);
        }
    }

    Serial.printf("queue=%u frames, pre-roll=%u frames, suppression=%d, settle=%ds\n",
                  static_cast<unsigned>(kQueueFrames), static_cast<unsigned>(kPreFrames),
                  SONIC_SUPPRESSION, SONIC_SETTLE_SECONDS);

    // Capture runs at a high priority with a small stack: it must never be
    // starved. Transmit owns WiFi and TCP and is allowed to block.
    xTaskCreatePinnedToCore(captureTask, "capture", 4096, nullptr, 10, nullptr, tskNO_AFFINITY);
    xTaskCreatePinnedToCore(transmitTask, "transmit", 8192, nullptr, 5, nullptr, tskNO_AFFINITY);
}

void loop() {
    delay(kStatusIntervalMs);

    Serial.printf("last_5s frames=%u dropped=%u audio_bytes=%u control_bytes=%u suppressed_ms=%u peak=%d rssi=%d\n",
                  static_cast<unsigned>(g_sentFrames.exchange(0)),
                  static_cast<unsigned>(g_droppedFrames.exchange(0)),
                  static_cast<unsigned>(g_audioBytes.exchange(0)),
                  static_cast<unsigned>(g_controlBytes.exchange(0)),
                  static_cast<unsigned>(g_quietFrames.exchange(0)*kFrameSamples*1000/kSampleRate),
                  g_peakSample.load(), WiFi.RSSI());
}
