// sonic-bridge ESP32-C3 firmware.
//
// Captures audio from an INMP441 I2S MEMS microphone, downshifts the 24-bit
// samples to 16-bit, and streams the raw PCM over TCP to the sonic-bridge
// server on SONIC_SERVER_HOST:SONIC_SERVER_PORT.
//
// Architecture:
//   I2S reader task --(ring buffer)--> TCP sender task --> server
// The two tasks are decoupled so that a slow / blocked TCP write can never
// stall i2s_channel_read(), which would otherwise overrun the I2S DMA and
// drop samples.
//
// Wire format (must match server + console):
//   16 kHz, 16-bit signed LE PCM, mono, raw byte stream.
//
// INMP441 wiring (mic pin -> ESP32-C3 GPIO):
//   VDD -> 3V3
//   GND -> GND
//   L/R -> GND          (selects the left I2S slot)
//   SCK -> SONIC_I2S_SCK    (default GPIO 4)
//   WS  -> SONIC_I2S_WS     (default GPIO 5)
//   SD  -> SONIC_I2S_SD     (default GPIO 6)
//
// Pin notes (ESP32-C3-DevKitM-1):
//   - GPIO 4/5/6 are safe general-purpose pins. Other free choices: 7, 8, 10.
//   - Avoid GPIO 11-17 (SPI flash), 18-19 (USB-JTAG on most modules),
//     20-21 (UART0 used by Serial), and the strapping pins 2/8/9.

#include <Arduino.h>
#include <WiFi.h>
#include <WiFiClient.h>
#include <lwip/sockets.h>

#include "driver/i2s_std.h"
#include "esp_err.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/ringbuf.h"

// === Edit these before flashing. ===
#define SONIC_WIFI_SSID     "WR7010-2.4G-82E"
#define SONIC_WIFI_PASS     "12345678"
#define SONIC_SERVER_HOST   "167.99.254.104"
#define SONIC_SERVER_PORT   9000

// === Tunables. Edit if you change the wire format, mic shift, or pinout. ===
#define SONIC_SAMPLE_RATE   16000
#define SONIC_CHUNK_SAMPLES 1024
#define SONIC_INMP441_SHIFT 14
#define SONIC_I2S_SCK       4
#define SONIC_I2S_WS        5
#define SONIC_I2S_SD        6

// DMA: 8 descriptors x 512 frames = 4096 frames = 256 ms of headroom.
#define SONIC_I2S_DMA_DESC_NUM   8
#define SONIC_I2S_DMA_FRAME_NUM  512

// Ring buffer between I2S task and TCP task. ~1 s of audio so a brief
// network stall doesn't lose samples. 16000 samples/s * 2 bytes = 32 KB.
#define SONIC_RING_BYTES         (SONIC_SAMPLE_RATE * 2)

// TCP send buffer hint for lwIP. Larger = more jitter tolerance.
#define SONIC_TCP_SNDBUF_BYTES   (16 * 1024)

// Short pause when the TCP connection has actually dropped and we need to
// rebuild it. Not used for one-off write hiccups.
#define SONIC_RECONNECT_MS       250

namespace {

constexpr int kSampleRate = SONIC_SAMPLE_RATE;
constexpr int kChunkSamples = SONIC_CHUNK_SAMPLES;
constexpr int kChunkBytes = kChunkSamples * 2;
constexpr int kI2sSckPin = SONIC_I2S_SCK;
constexpr int kI2sWsPin = SONIC_I2S_WS;
constexpr int kI2sSdPin = SONIC_I2S_SD;
constexpr int kInmpShift = SONIC_INMP441_SHIFT;
constexpr uint32_t kReconnectMs = SONIC_RECONNECT_MS;

i2s_chan_handle_t g_rx = nullptr;
RingbufHandle_t g_ring = nullptr;
WiFiClient g_client;

// Per-task scratch. Owned by the I2S task only.
int32_t g_i2s_buf[kChunkSamples];
int16_t g_send_buf[kChunkSamples];

bool setupI2s() {
    i2s_chan_config_t chan_cfg = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    chan_cfg.dma_desc_num = SONIC_I2S_DMA_DESC_NUM;
    chan_cfg.dma_frame_num = SONIC_I2S_DMA_FRAME_NUM;

    if (i2s_new_channel(&chan_cfg, nullptr, &g_rx) != ESP_OK) {
        Serial.println("i2s_new_channel failed");
        return false;
    }

    // We avoid the ESP-IDF helper macros (I2S_STD_CLK_DEFAULT_CONFIG etc.)
    // because their designated-initializer order does not match struct
    // declaration order for ESP32-C6, which is a hard error in C++.
    i2s_std_config_t std_cfg = {};

    std_cfg.clk_cfg.sample_rate_hz = kSampleRate;
    std_cfg.clk_cfg.clk_src = I2S_CLK_SRC_DEFAULT;
    std_cfg.clk_cfg.mclk_multiple = I2S_MCLK_MULTIPLE_256;

    std_cfg.slot_cfg.data_bit_width = I2S_DATA_BIT_WIDTH_32BIT;
    std_cfg.slot_cfg.slot_bit_width = I2S_SLOT_BIT_WIDTH_AUTO;
    std_cfg.slot_cfg.slot_mode = I2S_SLOT_MODE_MONO;
    // INMP441 L/R pin tied to GND -> data lands in the left slot.
    std_cfg.slot_cfg.slot_mask = I2S_STD_SLOT_LEFT;
    std_cfg.slot_cfg.ws_width = I2S_DATA_BIT_WIDTH_32BIT;
    std_cfg.slot_cfg.ws_pol = false;
    std_cfg.slot_cfg.bit_shift = true;
    std_cfg.slot_cfg.left_align = true;
    std_cfg.slot_cfg.big_endian = false;
    std_cfg.slot_cfg.bit_order_lsb = false;

    std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
    std_cfg.gpio_cfg.bclk = static_cast<gpio_num_t>(kI2sSckPin);
    std_cfg.gpio_cfg.ws   = static_cast<gpio_num_t>(kI2sWsPin);
    std_cfg.gpio_cfg.dout = I2S_GPIO_UNUSED;
    std_cfg.gpio_cfg.din  = static_cast<gpio_num_t>(kI2sSdPin);

    if (i2s_channel_init_std_mode(g_rx, &std_cfg) != ESP_OK) {
        Serial.println("i2s_channel_init_std_mode failed");
        return false;
    }
    if (i2s_channel_enable(g_rx) != ESP_OK) {
        Serial.println("i2s_channel_enable failed");
        return false;
    }
    return true;
}

void ensureWiFi() {
    if (WiFi.status() == WL_CONNECTED) return;
    Serial.printf("connecting WiFi: %s\n", SONIC_WIFI_SSID);
    WiFi.mode(WIFI_STA);
    WiFi.setSleep(false); // lower latency / fewer bursty stalls at the cost of power
    WiFi.begin(SONIC_WIFI_SSID, SONIC_WIFI_PASS);
    uint32_t deadline = millis() + 20000;
    while (WiFi.status() != WL_CONNECTED && millis() < deadline) {
        delay(200);
    }
    if (WiFi.status() == WL_CONNECTED) {
        Serial.printf("WiFi up, IP=%s, RSSI=%d\n", WiFi.localIP().toString().c_str(), WiFi.RSSI());
    } else {
        Serial.println("WiFi failed; will retry");
    }
}

bool ensureServer() {
    if (g_client.connected()) return true;
    Serial.printf("dialing server %s:%d\n", SONIC_SERVER_HOST, SONIC_SERVER_PORT);
    if (!g_client.connect(SONIC_SERVER_HOST, SONIC_SERVER_PORT)) {
        Serial.println("server connect failed");
        return false;
    }
    g_client.setNoDelay(true);
    int fd = g_client.fd();
    if (fd >= 0) {
        int sndbuf = SONIC_TCP_SNDBUF_BYTES;
        ::setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &sndbuf, sizeof(sndbuf));
    }
    Serial.println("server connected");
    return true;
}

// I2S reader task: blocks on i2s_channel_read, converts 32-bit slots to S16LE,
// and pushes converted bytes into g_ring. Never touches WiFi or TCP.
void i2sTask(void*) {
    for (;;) {
        size_t bytes_read = 0;
        esp_err_t r = i2s_channel_read(g_rx, g_i2s_buf, sizeof(g_i2s_buf), &bytes_read, portMAX_DELAY);
        if (r != ESP_OK) {
            Serial.printf("i2s read error: %d\n", r);
            vTaskDelay(pdMS_TO_TICKS(10));
            continue;
        }
        size_t samples = bytes_read / sizeof(int32_t);
        for (size_t i = 0; i < samples; ++i) {
            // INMP441 samples are 24-bit, MSB-aligned in the upper 24 bits of a
            // 32-bit slot. Right-shifting by 14 places the audio range into a
            // ~16-bit signed window with some headroom (tune via SONIC_INMP441_SHIFT).
            int32_t v = g_i2s_buf[i] >> kInmpShift;
            if (v > INT16_MAX) v = INT16_MAX;
            if (v < INT16_MIN) v = INT16_MIN;
            g_send_buf[i] = static_cast<int16_t>(v);
        }
        size_t bytes_to_send = samples * sizeof(int16_t);
        // Non-blocking send into the ring. If the ring is full the network task
        // is wedged; drop this chunk rather than block I2S.
        if (xRingbufferSend(g_ring, g_send_buf, bytes_to_send, 0) != pdTRUE) {
            static uint32_t s_drops = 0;
            if ((++s_drops % 16) == 1) {
                Serial.printf("ring full, dropping chunk (%u so far)\n", s_drops);
            }
        }
    }
}

// TCP sender task: ensures WiFi + server, then drains the ring into the socket.
// Blocking writes here cannot affect the I2S read cadence.
void tcpTask(void*) {
    for (;;) {
        ensureWiFi();
        if (WiFi.status() != WL_CONNECTED) {
            vTaskDelay(pdMS_TO_TICKS(kReconnectMs));
            continue;
        }
        if (!ensureServer()) {
            vTaskDelay(pdMS_TO_TICKS(kReconnectMs));
            continue;
        }

        size_t item_size = 0;
        // Wait up to 100 ms for a chunk; lets us re-check WiFi/server liveness.
        void* item = xRingbufferReceive(g_ring, &item_size, pdMS_TO_TICKS(100));
        if (item == nullptr) continue;

        const uint8_t* p = static_cast<const uint8_t*>(item);
        size_t remaining = item_size;
        bool ok = true;
        while (remaining > 0) {
            int n = g_client.write(p, remaining);
            if (n <= 0) {
                Serial.println("tcp write failed; reconnecting");
                g_client.stop();
                ok = false;
                break;
            }
            p += n;
            remaining -= static_cast<size_t>(n);
        }
        vRingbufferReturnItem(g_ring, item);

        if (!ok) {
            // Drain stale audio so we don't dump a backlog on the next connect.
            while ((item = xRingbufferReceive(g_ring, &item_size, 0)) != nullptr) {
                vRingbufferReturnItem(g_ring, item);
            }
            vTaskDelay(pdMS_TO_TICKS(kReconnectMs));
        }
    }
}

} // namespace

void setup() {
    Serial.begin(115200);
    delay(200);
    Serial.println("\nsonic-bridge esp32 starting");

    if (!setupI2s()) {
        Serial.println("FATAL: I2S setup failed; halting");
        while (true) delay(1000);
    }

    g_ring = xRingbufferCreate(SONIC_RING_BYTES, RINGBUF_TYPE_NOSPLIT);
    if (g_ring == nullptr) {
        Serial.println("FATAL: xRingbufferCreate failed; halting");
        while (true) delay(1000);
    }

    // I2S task: high priority, small stack. Must never be starved.
    xTaskCreatePinnedToCore(i2sTask, "i2s",   4096, nullptr, 10, nullptr, tskNO_AFFINITY);
    // TCP task: lower priority. Owns WiFi/TCP and may block.
    xTaskCreatePinnedToCore(tcpTask, "tcp",   8192, nullptr,  5, nullptr, tskNO_AFFINITY);
}

void loop() {
    // All work happens in the two tasks above. Nothing to do here.
    vTaskDelay(pdMS_TO_TICKS(1000));
}
