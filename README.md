# sonic-bridge

Live one-to-many audio. A **source** captures audio and streams it to the
**relay**, which fans it out to every connected **destination**.

```
            sources                        relay                 destinations

cmd/console  mic ──► encode ──┐
                              ├──► TCP :9000 ──► relay ──┬──► WS /listen ──► browser player
esp32/       INMP441 ─► encode ┘                         └──► GET /stream.wav ──► ffplay, VLC
```

Two sources, one relay, two ways to listen. Both sources speak the same wire,
so they can be read side by side. Every source declares its format on connect
rather than agreeing it by convention.

## Layout

| Path              | What it is                                                  |
| ----------------- | ----------------------------------------------------------- |
| `cmd/server`      | The relay. Serves the player, the WebSocket and the WAV.     |
| `cmd/console`     | A source. Captures a microphone or a test tone, in Go.       |
| `internal/audio`  | The wire format and the codecs. Shared by both binaries.     |
| `internal/queue`  | The project's backpressure policy: drop oldest, never block. |
| `internal/stream` | Fan-out from one source to many listeners.                   |
| `internal/relay`  | The relay's ingest, delivery, and the embedded player.       |
| `internal/source` | The console's capture pipeline.                              |
| `esp32/`          | ESP32-C3 firmware. A source, over raw TCP.                   |
| `scripts/`        | Deployment, and the cross-language wire format check.        |

## Quick start

Needs Go 1.25. There is no build step for the player and no Node in the
deployed artefact; Node is needed only to run `scripts/check-wire-format.sh`.

```bash
make server    # terminal 1: the relay on 127.0.0.1:8080
make tone      # terminal 2: a source streaming a 440 Hz test tone
```

Open <http://localhost:8080> and press **Listen**. You should hear 440 Hz. That
proves the relay, the transport and the browser's decode path all work before
any microphone is involved.

Then swap the tone for a real microphone:

```bash
make console            # stream this machine's default microphone
make devices            # list capture devices
go run ./cmd/console --server 127.0.0.1:9000 --device "MacBook" --gain 2
```

To listen without a browser:

```bash
ffplay -nodisp http://127.0.0.1:8080/stream.wav
```

`make help` lists every target.

## Platform support

Read this before relying on either listening path.

| Path                      | Chrome, Firefox, Edge | Safari and iOS    |
| ------------------------- | --------------------- | ----------------- |
| The player (`WS /listen`) | Works                 | Works             |
| `GET /stream.wav`         | Works                 | **Does not work** |

`/stream.wav` is a chunked response with no `Content-Length`. Safari hands a
media element to AVFoundation, which wants either a declared length or genuine
byte-range support, and a live stream can offer neither: it has no retained
past and no future, so the only offset it could truthfully serve is "now".
**`/stream.wav` is for `ffplay`, VLC, `curl` and `ffmpeg`, not for browsers.**

### The locked iPhone, not yet settled

Foreground listening on an iPhone is confirmed working. Playback **with the
screen locked is not confirmed**, and there is a specific reason to expect
trouble. WebKit registers `BackgroundProcessPlaybackRestricted` against
`MediaType::WebAudio`, and `MediaElementSession::visibilityChanged()` interrupts
a session that is not actively producing audio when the page becomes hidden. The
player is a Web Audio graph feeding an `<audio>` element through `srcObject`,
which is the shape those restrictions target.

To check: press Listen, lock the screen, wait five minutes.

If it does not hold, the fix is to serve a progressive MP3 to a bare
`<audio src="/stream.mp3">`, because `MediaType::Audio` carries no background
restriction at all, which is why internet radio has worked on iPhones for
twenty years. That is a larger change: an MP3 encoder in the relay, encoded
silence so the response never ends, and a burst of frames on connect to fill
the player's pre-buffer.

## The wire format

A stream is **mono, linear-PCM-derived audio in fixed-size frames**. The codec,
sample rate and frame size are declared by the source, so the relay and the
destinations never guess.

### The codec

**mu-law is the only codec on the wire.** ITU-T G.711 mu-law is one byte per
sample, so 128 kbps at 16 kHz, half the cost of linear 16-bit samples. It costs
about 38 dB of signal-to-noise ratio, which is inaudible for speech.

`internal/audio` also exports `EncodePcm16` and `DecodePcm16`. Those are not a
wire codec: they are the linear byte layout that WAV carries and that capture
devices produce, and they exist because the WAV endpoint and the console's
device capture both need to convert between bytes and samples.

### Ingest: the stream header

TCP is the relay's only ingest transport. A source sends 12 bytes once, before
any audio:

| Offset | Size | Field         | Value                     |
| ------ | ---- | ------------- | ------------------------- |
| 0      | 4    | magic         | `SB01`                    |
| 4      | 1    | codec id      | 1 = `mulaw`, the only value accepted |
| 5      | 1    | channels      | 1                         |
| 6      | 2    | frame samples | uint16, little-endian     |
| 8      | 4    | sample rate   | uint32, little-endian     |

Then frames back to back, each exactly `frameSamples` bytes, since mu-law is
one byte per sample. There is no per-frame framing: the header fixes the size.

The relay accepts one source at a time. A second source is closed immediately
after its header.

### Delivery: the listener protocol

A destination connects to `WS /listen` and receives a JSON text message
whenever the format changes, then one binary message per frame:

```json
{ "codec": "mulaw", "sampleRate": 16000, "channels": 1, "frameSamples": 320 }
```

The relay does not transcode on the live path: it forwards the source's bytes
along with the format that describes them, and destinations decode. The one
exception is `/stream.wav`, because WAV carries linear PCM only.

### Why declared and not agreed

Before this, three files carried a comment saying 16 kHz while all three
constants said 8000, and they pointed at a format specification in this README
that did not exist. With a codec in the pipeline that class of drift stops
being cosmetic: decode a mu-law frame as PCM and you get noise, not an error.

`scripts/check-wire-format.sh` proves the implementations agree where they
overlap. Go is the hub, so it checks two pairs rather than one three-way match:

```
  encode  Go   cc026405e617f1c15d1c71f9a9c2fe4575184c4a7032e1d83b52b6320e5cf9e1
          C++  cc026405e617f1c15d1c71f9a9c2fe4575184c4a7032e1d83b52b6320e5cf9e1

  decode  Go   3dab54339e520bb2c924826e3b72a917a2b612e9fd12fc867500f1d983a75827
          JS   3dab54339e520bb2c924826e3b72a917a2b612e9fd12fc867500f1d983a75827
```

The firmware encodes only, the browser decodes only, Go does both. Nothing
needs C++ and JS to agree directly, because no firmware byte reaches a browser
without passing through Go. The script needs Go, Node and a C++ compiler. It
does not need an ESP32 or PlatformIO, because `esp32/src/wire.h` has no
framework dependencies.

## Relay endpoints

| Endpoint            | Purpose                                            |
| ------------------- | -------------------------------------------------- |
| `GET /`             | The browser player, embedded in the binary.        |
| `GET /healthz`      | Liveness. Also `server --health-check`.            |
| `GET /stats`        | Format, listener count, frames, drops.             |
| `GET /listen`       | WebSocket delivery. Low latency.                   |
| `GET /stream.wav`   | Endless WAV. For media players, not browsers.      |
| TCP `:9000`         | Source ingest.                                     |

## How a source is built

Both sources run the same three stages in the same order:

| Stage    | `internal/source`      | `esp32/src/main.cpp` |
| -------- | ---------------------- | -------------------- |
| capture  | miniaudio, or the tone | I2S reader task      |
| encode   | `audio.Codec.Encode`   | `sonic::encodeFrame` |
| transmit | raw TCP                | TCP task             |

They also share one policy: **capture never blocks on the network.** A full
buffer drops its oldest frame instead of stalling the capture, because a
blocked audio callback overruns the driver and corrupts the capture, and stale
audio is worth less than live audio. See `internal/queue`.

The firmware deviates in one documented way: it encodes *before* the buffer
that separates its two tasks, not after. RAM is the scarce resource on a
microcontroller, so compressing first doubles the jitter tolerance the same
buffer provides.

## The player

One hand-written HTML file plus two scripts, embedded in the relay binary. No
framework, no bundler, no build step:

```
internal/relay/web/index.html              page, inline CSS, inline module script
internal/relay/web/codec.js                mu-law decode
internal/relay/web/playback-processor.js   AudioWorklet jitter buffer
```

**The page is one button.** Press it and frames arrive over a WebSocket, are
decoded, and pass through the worklet into a `MediaStreamAudioDestinationNode`
that a hidden `<audio>` element plays via `srcObject`. Volume is the phone's
hardware buttons. Media Session metadata puts a title on the lock screen
instead of a URL.

Four things in that page are load-bearing and easy to break by accident:

- The `AudioContext`, the stream destination and `play()` are created
  **synchronously inside the click handler**. Awaiting anything first loses the
  user gesture on iOS, and the result is silence with no error.
- The relay's JSON format message is validated rather than trusted, so an
  unplayable stream reports itself instead of decoding into noise.
- A sample rate that differs from the graph rebuilds the graph, because a
  jitter buffer cannot resample.
- The socket reconnects with exponential backoff, so opening the page before
  any source exists recovers on its own.

`codec.js` is a separate module rather than inline script for one reason:
`scripts/check-wire-format.sh` imports it in Node to prove it matches the Go
encoder. A decoder that drifts produces noise, not an error.

To change the player, edit the file and restart the relay. There is no dev
server and no hot reload; the restart takes about a second.

## The ESP32 source

Captures an INMP441 I2S microphone and streams over raw TCP.

```bash
cp esp32/secrets.ini.example esp32/secrets.ini   # then fill it in
pio run -t upload -d esp32
pio device monitor -d esp32
```

`secrets.ini` holds WiFi credentials, the relay address and the wire format. It
is not tracked by git. Wiring and pin notes are at the top of
`esp32/src/main.cpp`.

The TCP source port is unauthenticated plain TCP, and it is the relay's **only**
ingest path: the console uses it too, and neither speaks TLS. Anyone who can
reach it can publish to the stream. Firewall it to the addresses your sources
use, or keep it closed and tunnel:

```bash
ssh -L 9000:127.0.0.1:9000 root@droplet
./bin/console --server 127.0.0.1:9000
```

## Deploying

One container: the relay serves the player from its own binary, so there is no
separate static file server and no internal hop. The image is about 15 MB and
runs as a non-root user on a distroless base.

On a fresh Ubuntu Droplet, with `DOMAIN`'s A record already pointing at it:

```bash
cp .env.example .env     # set DOMAIN and LETSENCRYPT_EMAIL
scp .env root@droplet:/root/.env
ssh root@droplet
git clone https://github.com/hirebarend/sonic-bridge && cd sonic-bridge
sudo ./scripts/setup-digitalocean.sh --env-file /root/.env
```

That installs Docker, fetches `compose.yaml`, opens 80, 443 and the TCP source
port in `ufw` when it is active, and brings up Traefik with a Let's Encrypt
certificate. DNS has to resolve first: the certificate is obtained over an
HTTP-01 challenge.

Afterwards the relay keeps itself on the newest image published for the
deployed tag. Pin a commit tag to stop that and to roll back:

```bash
sudo ./scripts/setup-digitalocean.sh --tag sha-2e6a4c9
```

Locally, `make image` then:

```bash
docker run --rm -p 8080:8080 -p 9000:9000 sonic-bridge:local
```

## Tuning

There are eight command-line flags in total, three on the relay and five on the
console. Everything else is a named constant, so tuning means editing one line
and rebuilding. That is deliberate: a flag nobody sets is a maintenance cost
and a false promise of configurability.

| Constant                  | Where                             | Default | Effect                                    |
| ------------------------- | --------------------------------- | ------- | ----------------------------------------- |
| `frameSamples`            | `internal/source/source.go`       | 320     | 20 ms at 16 kHz. Lower cuts latency.       |
| `sampleRate`              | `internal/source/source.go`       | 16000   | What the console declares.                 |
| `queueDepth`              | `internal/relay/relay.go`         | 48      | Frames buffered per listener before drops. |
| `PREFILL_MILLISECONDS`    | `internal/relay/web/index.html`   | 120     | Jitter absorbed before playback starts.    |
| `SONIC_FRAME_SAMPLES`     | `esp32/secrets.ini`               | 320     | The firmware's frame size.                 |

End-to-end latency is roughly one frame, plus the player's jitter buffer, plus
the network. The honest failure mode is visible in the interface: a network
that cannot keep up shows a rising **gaps** count, and a relay that cannot keep
up shows drops in `/stats` and in its log.

## Testing

```bash
make check    # gofmt, vet, race tests, cgo-free build, player syntax
make wire     # the Go, C++ and JavaScript implementations agree
```

The Go tests cover the codecs against the G.711 standard anchors and over all
256 codes, the WAV header's signed-int32 size limits, the header round trip,
the fan-out under `-race` with listeners attaching and detaching while a source
publishes, and the relay end to end: TCP ingest over an in-memory pipe, format
negotiation, one-source-at-a-time arbitration, WebSocket delivery, and a
Goertzel measurement proving a published 440 Hz tone arrives at `/stream.wav`
as a recognisable 440 Hz sine rather than merely as bytes.

`make check` does not run `make wire`, because the wire check needs Node and a
C++ compiler that a Go-only contributor may not have. CI runs both.
