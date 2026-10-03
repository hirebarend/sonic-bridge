package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"sonic-bridge/internal/audio"
)

func sparseSource(t *testing.T, ctx context.Context, r *Relay) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go r.readTcpSource(ctx, server)
	// Fragment the stream header, as real TCP is permitted to do.
	for _, b := range testFormat().BuildSparseHeader() {
		if _, err := client.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	waitForSource(t, r)
	return client
}

func readMessage(t *testing.T, ctx context.Context, c *websocket.Conn, want websocket.MessageType) []byte {
	t.Helper()
	kind, payload, err := c.Read(ctx)
	if err != nil || kind != want {
		t.Fatalf("read: kind=%v error=%v want=%v", kind, err, want)
	}
	return payload
}

func TestSparseQuietJoinResumeAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, server := startRelay(t)
	listener := dialListener(t, ctx, server)
	source := sparseSource(t, ctx, r)
	var format struct {
		audio.Format
		Epoch  uint64
		Sparse bool
		State  string
	}
	if err := json.Unmarshal(readMessage(t, ctx, listener, websocket.MessageText), &format); err != nil {
		t.Fatal(err)
	}
	if !format.Sparse || format.Epoch == 0 || format.State != "live" {
		t.Fatal(format)
	}
	quiet := audio.Record{Position: 0}
	for _, b := range quiet.Bytes() {
		if _, err := source.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if b := readMessage(t, ctx, listener, websocket.MessageBinary); !bytes.Equal(b, quiet.Bytes()) {
		t.Fatal("quiet record changed")
	}
	late := dialListener(t, ctx, server)
	if err := json.Unmarshal(readMessage(t, ctx, late, websocket.MessageText), &format); err != nil {
		t.Fatal(err)
	}
	if format.State != "quiet" {
		t.Fatal("quiet join waited for activity")
	}
	readMessage(t, ctx, late, websocket.MessageBinary)
	// A ten-minute suppressed timeline has only 120 tiny heartbeat records.
	for i := 1; i <= 120; i++ {
		heartbeat := audio.Record{Position: uint64(i * 80000)}
		if _, err := source.Write(heartbeat.Bytes()); err != nil {
			t.Fatal(err)
		}
		if b := readMessage(t, ctx, listener, websocket.MessageBinary); len(b) != 9 {
			t.Fatal("audio during silence")
		}
		readMessage(t, ctx, late, websocket.MessageBinary)
	}
	frame := audio.Record{Position: 120*80000 + 320, Frame: bytes.Repeat([]byte{0x80}, 320)}
	if _, err := source.Write(frame.Bytes()); err != nil {
		t.Fatal(err)
	}
	if b := readMessage(t, ctx, listener, websocket.MessageBinary); !bytes.Equal(b, frame.Bytes()) {
		t.Fatal("resume changed audio")
	}
	readMessage(t, ctx, late, websocket.MessageBinary)
	stats := readStats(t, server)
	if stats.IngressAudioBytes != 320 || stats.PublishedFrames != 1 || stats.SuppressedSeconds < 600 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.IngressControlBytes != 12+122*9 {
		t.Fatalf("unexpected control bytes %d", stats.IngressControlBytes)
	}
	listener.CloseNow()
	late.CloseNow()
	deadline := time.Now().Add(time.Second)
	for r.stream.ListenerCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.stream.ListenerCount() != 0 {
		t.Fatal("closed quiet listeners leaked")
	}
	source.Close()
}

func TestSparseWavSynthesizesPacedSilence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, server := startRelay(t)
	source := sparseSource(t, ctx, r)
	if _, err := source.Write((audio.Record{}).Bytes()); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/stream.wav", nil)
	start := time.Now()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	payload := make([]byte, audio.WavHeaderBytes+3*640)
	if _, err := io.ReadFull(response.Body, payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload[audio.WavHeaderBytes:], make([]byte, 3*640)) {
		t.Fatal("WAV silence is not PCM zeros")
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("silence was burst instead of paced")
	}
	frame := audio.Record{Position: 1600, Frame: bytes.Repeat([]byte{0x80}, 320)}
	if _, err := source.Write(frame.Bytes()); err != nil {
		t.Fatal(err)
	}
	// A timer tick may have raced the event. Audio must arrive within two frames.
	found := false
	for range 3 {
		b := make([]byte, 640)
		if _, err := io.ReadFull(response.Body, b); err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(b, audio.EncodePcm16(audio.DecodeMulaw(frame.Frame))) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("WAV did not resume")
	}
}

func TestSparseRejectsInvalidOrBackwardRecords(t *testing.T) {
	for _, kind := range []string{"backward", "unknown", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, _ := startRelay(t)
			source := sparseSource(t, ctx, r)
			if _, err := source.Write((audio.Record{Position: 320}).Bytes()); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "backward":
				source.Write((audio.Record{}).Bytes())
			case "unknown":
				b := (audio.Record{Position: 640}).Bytes()
				b[0] = 99
				source.Write(b)
			case "truncated":
				source.Write([]byte{1, 0})
				source.Close()
			}
			deadline := time.Now().Add(time.Second)
			for r.stream.FindFormat() != nil && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if r.stream.FindFormat() != nil {
				t.Fatal("invalid source retained stream")
			}
		})
	}
}

func TestLegacyBrowserCannotDecodeSparseHeadersAsAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, server := startRelay(t)
	source := sparseSource(t, ctx, r)
	defer source.Close()
	listener, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/listen", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.CloseNow()
	_, _, err = listener.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("expected reload-required close, got %v", err)
	}
}

func TestQuietHeartbeatsKeepSourceAliveThenExpire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, _ := startRelay(t)
	r.sourceTimeout = 200 * time.Millisecond
	source := sparseSource(t, ctx, r)
	for i := 0; i < 8; i++ {
		if _, err := source.Write((audio.Record{Position: uint64(i * 80000)}).Bytes()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.stream.FindFormat() == nil {
		t.Fatal("healthy quiet source expired")
	}
	deadline := time.Now().Add(time.Second)
	for r.stream.FindFormat() != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.stream.FindFormat() != nil {
		t.Fatal("source without heartbeats never expired")
	}
}
