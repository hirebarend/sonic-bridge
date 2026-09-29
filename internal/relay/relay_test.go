package relay

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"sonic-bridge/internal/audio"
)

func testFormat() audio.Format {
	return audio.Format{Codec: audio.CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
}

func startRelay(t *testing.T) (*Relay, *httptest.Server) {
	t.Helper()

	r := New(Config{}, slog.New(slog.DiscardHandler))
	// Shortened so the no-source case fails fast instead of waiting the
	// production ten seconds.
	r.sourceTimeout = 300 * time.Millisecond

	server := httptest.NewServer(r.Handler())
	t.Cleanup(server.Close)

	return r, server
}

func dialListener(t *testing.T, ctx context.Context, server *httptest.Server) *websocket.Conn {
	t.Helper()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/listen"

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial /listen: %v", err)
	}

	t.Cleanup(func() { _ = conn.CloseNow() })

	return conn
}

// openTcpSource drives the relay's TCP ingest over an in-memory pipe, so tests
// never bind a port and cannot flake on one being taken. Writing a frame to
// the returned connection publishes it.
func openTcpSource(t *testing.T, ctx context.Context, r *Relay, format audio.Format) net.Conn {
	t.Helper()

	client, relaySide := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })

	go r.readTcpSource(ctx, relaySide)

	if _, err := client.Write(format.BuildHeader()); err != nil {
		t.Fatalf("write header: %v", err)
	}

	waitForSource(t, r)

	return client
}

func waitForSource(t *testing.T, r *Relay) {
	t.Helper()

	for range 400 {
		if r.stream.FindFormat() != nil {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("the relay never acquired the source")
}

// publishTone streams a continuous 440 Hz mu-law tone until ctx is cancelled.
// Phase carries across frames, so the result is a single unbroken sine.
func publishTone(ctx context.Context, conn net.Conn, format audio.Format) {
	tone := audio.NewTone(format.SampleRate, 440, 0.5)
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := conn.Write(audio.EncodeMulaw(tone.Next(format.FrameSamples))); err != nil {
				return
			}
		}
	}
}

func TestHealthIsOk(t *testing.T) {
	_, server := startRelay(t)

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

func readStats(t *testing.T, server *httptest.Server) streamStatus {
	t.Helper()

	response, err := server.Client().Get(server.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var status streamStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}

	return status
}

func TestStatsReportsAnIdleStream(t *testing.T) {
	_, server := startRelay(t)

	status := readStats(t, server)

	if status.Format != nil {
		t.Errorf("format = %+v on an idle stream, want null", status.Format)
	}

	if status.Listeners != 0 || status.PublishedFrames != 0 || status.DroppedFrames != 0 {
		t.Errorf("got %+v, want all counters zero", status)
	}
}

func TestStatsReportsTheNegotiatedFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, server := startRelay(t)
	// A format the relay has no default for, so this proves negotiation rather
	// than a coincidence.
	format := audio.Format{Codec: audio.CodecMulaw, SampleRate: 8000, Channels: 1, FrameSamples: 160}
	openTcpSource(t, ctx, r, format)

	status := readStats(t, server)

	if status.Format == nil || *status.Format != format {
		t.Fatalf("format = %v, want %+v", status.Format, format)
	}
}

func TestTcpSourceReachesWebSocketListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, server := startRelay(t)
	listener := dialListener(t, ctx, server)
	source := openTcpSource(t, ctx, r, testFormat())

	frame := audio.EncodeMulaw(audio.NewTone(16000, 440, 0.5).Next(320))

	if _, err := source.Write(frame); err != nil {
		t.Fatalf("publish frame: %v", err)
	}

	messageType, payload, err := listener.Read(ctx)
	if err != nil {
		t.Fatalf("read format announcement: %v", err)
	}

	if messageType != websocket.MessageText {
		t.Fatalf("first message is %v, want a text format announcement", messageType)
	}

	var announced audio.Format
	if err := json.Unmarshal(payload, &announced); err != nil {
		t.Fatal(err)
	}

	if announced != testFormat() {
		t.Fatalf("announced %+v, want %+v", announced, testFormat())
	}

	messageType, received, err := listener.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	if messageType != websocket.MessageBinary {
		t.Fatalf("second message is %v, want binary", messageType)
	}

	if string(received) != string(frame) {
		t.Fatal("the relayed frame does not match the published frame")
	}
}

func TestTcpSourceWithoutAHeaderIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r, _ := startRelay(t)

	client, relaySide := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		r.readTcpSource(ctx, relaySide)
		close(done)
	}()

	if _, err := client.Write(make([]byte, audio.HeaderBytes)); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the relay accepted a zeroed header instead of closing the connection")
	}

	if r.stream.FindFormat() != nil {
		t.Fatal("a rejected source must not hold the stream")
	}
}

// One source at a time is the relay's contract. This replaces the coverage the
// deleted WebSocket-source test used to provide.
func TestSecondTcpSourceIsTurnedAway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, _ := startRelay(t)
	openTcpSource(t, ctx, r, testFormat())

	second, relaySide := net.Pipe()
	defer second.Close()

	done := make(chan struct{})
	go func() {
		r.readTcpSource(ctx, relaySide)
		close(done)
	}()

	if _, err := second.Write(testFormat().BuildHeader()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the relay accepted a second source while the first held the stream")
	}

	if format := r.stream.FindFormat(); format == nil {
		t.Fatal("rejecting the second source must not release the first")
	}
}

// The WAV endpoint is the only place the relay decodes, so this proves a
// mu-law source reaches a media player as linear PCM.
func TestWavStreamDecodesMulawToLinearPcm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, server := startRelay(t)
	source := openTcpSource(t, ctx, r, testFormat())

	samples := audio.NewTone(16000, 440, 0.5).Next(320)
	frame := audio.EncodeMulaw(samples)

	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := source.Write(frame); err != nil {
					return
				}
			}
		}
	}()

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/stream.wav", nil)

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("Content-Type = %q, want audio/wav", got)
	}

	header := make([]byte, audio.WavHeaderBytes)
	if _, err := io.ReadFull(response.Body, header); err != nil {
		t.Fatalf("read wav header: %v", err)
	}

	if string(header[0:4]) != "RIFF" {
		t.Fatalf("got %q, want a RIFF header", header[0:4])
	}

	if got := binary.LittleEndian.Uint32(header[24:]); got != 16000 {
		t.Fatalf("wav sample rate = %d, want 16000", got)
	}

	payload := make([]byte, 640)
	if _, err := io.ReadFull(response.Body, payload); err != nil {
		t.Fatalf("read wav payload: %v", err)
	}

	want := audio.EncodePcm16(audio.DecodeMulaw(frame))
	if string(payload) != string(want) {
		t.Fatal("the WAV payload is not the mu-law frame decoded to linear PCM")
	}
}

// Asserting bytes arrived is not the same as asserting the audio is right.
// This measures the delivered signal: a 440 Hz tone published as mu-law must
// come out of the WAV endpoint as a recognisable 440 Hz sine.
func TestWavStreamDeliversACleanTone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	r, server := startRelay(t)
	format := testFormat()
	source := openTcpSource(t, ctx, r, format)

	go publishTone(ctx, source, format)

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/stream.wav", nil)

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if _, err := io.ReadFull(response.Body, make([]byte, audio.WavHeaderBytes)); err != nil {
		t.Fatalf("read wav header: %v", err)
	}

	const wanted = 8192
	payload := make([]byte, wanted*2)
	if _, err := io.ReadFull(response.Body, payload); err != nil {
		t.Fatalf("read wav payload: %v", err)
	}

	samples := audio.DecodePcm16(payload)

	onPitch := goertzelPower(samples, format.SampleRate, 440)
	offPitch := goertzelPower(samples, format.SampleRate, 1000)

	if onPitch <= offPitch*100 {
		t.Fatalf("440 Hz power %.3g is not dominant over 1000 Hz power %.3g", onPitch, offPitch)
	}

	if status := readStats(t, server); status.DroppedFrames != 0 {
		t.Errorf("dropped %d frames while delivering the tone, want 0", status.DroppedFrames)
	}
}

// goertzelPower is the energy at one frequency, which is all that is needed to
// tell a clean tone from noise without a full transform.
func goertzelPower(samples []int16, sampleRate int, frequency float64) float64 {
	coefficient := 2 * math.Cos(2*math.Pi*frequency/float64(sampleRate))

	var previous, older float64
	for _, sample := range samples {
		current := float64(sample) + coefficient*previous - older
		older = previous
		previous = current
	}

	return previous*previous + older*older - coefficient*previous*older
}

func TestWavStreamIsUnavailableWithoutASource(t *testing.T) {
	_, server := startRelay(t)

	response, err := server.Client().Get(server.URL + "/stream.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
}

func TestPlayerIsServed(t *testing.T) {
	_, server := startRelay(t)

	response, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", got)
	}

	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	for _, reference := range []string{"./codec.js", "./playback-processor.js"} {
		if !strings.Contains(string(body), reference) {
			t.Errorf("the page does not reference %s", reference)
		}
	}
}

// AudioWorklet.addModule refuses a module that is not served as JavaScript, so
// the content type is part of the contract, not a detail.
func TestWorkletIsServedAsJavaScript(t *testing.T) {
	_, server := startRelay(t)

	for _, path := range []string{"/playback-processor.js", "/codec.js"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		response.Body.Close()

		if response.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, response.StatusCode)
		}

		if got := response.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Errorf("%s: Content-Type = %q, want a JavaScript type", path, got)
		}
	}
}

// A catch-all would return the page with status 200 for every typo.
func TestUnknownPathIsNotFound(t *testing.T) {
	_, server := startRelay(t)

	response, err := server.Client().Get(server.URL + "/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
}
