package audio

import (
	"os"
	"testing"
)

// These two tests are the Go side of scripts/check-wire-format.sh. They do
// nothing unless that script asks by setting an output path, so an ordinary
// `go test` skips them. Emitting the artefacts from a committed test is what
// lets the script avoid writing a temporary test file into the source tree and
// deleting it afterwards.

func TestWriteWireEncodeArtefact(t *testing.T) {
	path := os.Getenv("SONIC_WIRE_ENCODE_OUT")
	if path == "" {
		t.Skip("set SONIC_WIRE_ENCODE_OUT to write the artefact")
	}

	samples := make([]int16, 65536)
	for i := range samples {
		samples[i] = int16(i - 32768)
	}

	format := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
	payload := append(EncodeMulaw(samples), format.BuildHeader()...)
	payload = append(payload, format.BuildSparseHeader()...)
	payload = append(payload, (Record{Position: 80000}).Bytes()...)
	frame := make([]byte, 320)
	for i := range frame {
		frame[i] = byte(i)
	}
	payload = append(payload, (Record{Position: 80320, Frame: frame}).Bytes()...)

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteWireDecodeArtefact(t *testing.T) {
	path := os.Getenv("SONIC_WIRE_DECODE_OUT")
	if path == "" {
		t.Skip("set SONIC_WIRE_DECODE_OUT to write the artefact")
	}

	codes := make([]byte, 256)
	for i := range codes {
		codes[i] = byte(i)
	}

	payload := EncodePcm16(DecodeMulaw(codes))

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}
