package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"sonic-bridge/internal/audio"
)

func TestEightKHzBatchedSB01(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, server := startRelay(t)
	listener := dialListener(t, ctx, server)
	format := audio.Format{Codec: audio.CodecMulaw, SampleRate: 8000, Channels: 1, FrameSamples: 320}
	source := openTcpSource(t, ctx, r, format)
	first, second := bytes.Repeat([]byte{0xff}, 320), bytes.Repeat([]byte{0x80}, 320)
	batch := append(append([]byte{}, first...), second...)
	if _, err := source.Write(batch); err != nil {
		t.Fatal(err)
	}
	var announced struct {
		audio.Format
		Sparse bool
	}
	if err := json.Unmarshal(readMessage(t, ctx, listener, websocket.MessageText), &announced); err != nil {
		t.Fatal(err)
	}
	if announced.Format != format || announced.Sparse || format.FrameDuration() != 40*time.Millisecond {
		t.Fatalf("wrong playback format: %+v", announced)
	}
	for _, want := range [][]byte{first, second} {
		if got := readMessage(t, ctx, listener, websocket.MessageBinary); !bytes.Equal(got, want) {
			t.Fatalf("batched TCP write changed frame: got %d bytes", len(got))
		}
	}
	wav := audio.BuildLiveWavHeader(format.SampleRate, format.Channels)
	if binary.LittleEndian.Uint32(wav[24:]) != 8000 || binary.LittleEndian.Uint32(wav[28:]) != 16000 {
		t.Fatal("WAV sample/byte rate does not match 8 kHz PCM")
	}
}
