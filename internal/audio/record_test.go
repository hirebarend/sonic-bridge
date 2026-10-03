package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestSparseRecords(t *testing.T) {
	f := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
	parsed, err := ParseHeader(f.BuildSparseHeader())
	if err != nil || parsed != f {
		t.Fatal(parsed, err)
	}
	for _, r := range []Record{{Position: 320}, {Position: 640, Frame: bytes.Repeat([]byte{255}, 320)}} {
		b := r.Bytes()
		got, err := ReadRecord(bytes.NewBuffer(b), f)
		if err != nil || got.Position != r.Position || !bytes.Equal(got.Frame, r.Frame) {
			t.Fatal(got, err)
		}
		for n := 0; n < len(b); n++ {
			if _, err := ReadRecord(bytes.NewReader(b[:n]), f); err == nil {
				t.Fatalf("accepted truncation %d", n)
			}
		}
	}
	for _, kind := range []byte{0, 3, 255} {
		b := make([]byte, 9)
		b[0] = kind
		if _, err := ReadRecord(bytes.NewReader(b), f); err == nil {
			t.Fatal("accepted unknown kind")
		}
	}
	for _, pos := range []uint64{1, MaxPosition + 1, ^uint64(0)} {
		b := make([]byte, 9)
		b[0] = 2
		binary.LittleEndian.PutUint64(b[1:], pos)
		if _, err := ReadRecord(bytes.NewReader(b), f); err == nil {
			t.Fatal("accepted invalid position")
		}
	}
	if _, err := ReadRecord(bytes.NewReader(nil), f); err != io.EOF {
		t.Fatal(err)
	}
}
