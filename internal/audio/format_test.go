package audio

import (
	"testing"
	"time"
)

func TestHeaderRoundTripPreservesFormat(t *testing.T) {
	want := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}

	got, err := ParseHeader(want.BuildHeader())
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}

	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestBuildHeaderIsFixedWidth(t *testing.T) {
	format := Format{Codec: CodecMulaw, SampleRate: 8000, Channels: 1, FrameSamples: 1024}

	if got := len(format.BuildHeader()); got != HeaderBytes {
		t.Fatalf("header is %d bytes, want %d", got, HeaderBytes)
	}
}

// A client speaking the old headerless protocol sends raw PCM, which must be
// rejected rather than interpreted as a format.
func TestParseHeaderRejectsRawPcm(t *testing.T) {
	if _, err := ParseHeader(make([]byte, HeaderBytes)); err == nil {
		t.Fatal("expected an error for a zeroed header")
	}
}

func TestParseHeaderRejectsShortInput(t *testing.T) {
	if _, err := ParseHeader([]byte("SB01")); err == nil {
		t.Fatal("expected an error for a truncated header")
	}
}

func TestValidateRejectsUnusableFormats(t *testing.T) {
	cases := map[string]Format{
		"unknown codec": {Codec: "opus", SampleRate: 16000, Channels: 1, FrameSamples: 320},
		"stereo":        {Codec: CodecMulaw, SampleRate: 16000, Channels: 2, FrameSamples: 320},
		"rate too low":  {Codec: CodecMulaw, SampleRate: 100, Channels: 1, FrameSamples: 320},
		"rate too high": {Codec: CodecMulaw, SampleRate: 192000, Channels: 1, FrameSamples: 320},
		"no samples":    {Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 0},
	}

	for name, format := range cases {
		if err := format.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestFrameBytesIsOneBytePerSample(t *testing.T) {
	format := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}

	if got := format.FrameBytes(); got != 320 {
		t.Errorf("FrameBytes = %d, want 320", got)
	}
}

func TestFrameDurationMatchesSampleRate(t *testing.T) {
	format := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}

	if got := format.FrameDuration(); got != 20*time.Millisecond {
		t.Fatalf("got %v, want 20ms", got)
	}
}
