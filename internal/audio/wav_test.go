package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestLiveWavHeaderDescribesTheStream(t *testing.T) {
	header := BuildLiveWavHeader(16000, 1)

	if len(header) != WavHeaderBytes {
		t.Fatalf("header is %d bytes, want %d", len(header), WavHeaderBytes)
	}

	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		t.Fatalf("got %q, want a RIFF/WAVE header", header[0:12])
	}

	if got := binary.LittleEndian.Uint16(header[20:]); got != 1 {
		t.Errorf("audio format = %d, want 1 (PCM)", got)
	}

	if got := binary.LittleEndian.Uint32(header[24:]); got != 16000 {
		t.Errorf("sample rate = %d, want 16000", got)
	}

	if got := binary.LittleEndian.Uint32(header[28:]); got != 32000 {
		t.Errorf("byte rate = %d, want 32000", got)
	}

	if got := binary.LittleEndian.Uint16(header[34:]); got != 16 {
		t.Errorf("bits per sample = %d, want 16", got)
	}
}

// Many WAV parsers read RIFF chunk sizes as signed int32, where anything above
// 0x7FFFFFFF is negative. Both size fields must stay inside positive range,
// and the RIFF field is the larger of the two because it includes the header.
func TestLiveWavHeaderSizesStayInPositiveInt32Range(t *testing.T) {
	header := BuildLiveWavHeader(16000, 1)

	riffSize := binary.LittleEndian.Uint32(header[4:])
	dataSize := binary.LittleEndian.Uint32(header[40:])

	if riffSize > math.MaxInt32 {
		t.Errorf("RIFF size %#x exceeds MaxInt32, so a signed parser reads it as negative", riffSize)
	}

	if dataSize > math.MaxInt32 {
		t.Errorf("data size %#x exceeds MaxInt32, so a signed parser reads it as negative", dataSize)
	}

	if riffSize <= dataSize {
		t.Errorf("RIFF size %#x must exceed data size %#x, it includes the header", riffSize, dataSize)
	}
}
