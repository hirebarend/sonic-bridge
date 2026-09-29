// Package audio defines the sonic-bridge wire format and the sample codecs
// that sources, the relay, and destinations agree on. Nothing in this package
// performs I/O.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// CodecName identifies a codec on the wire. It is the same token in the binary
// TCP header and in the JSON WebSocket handshake.
type CodecName string

// CodecMulaw is ITU-T G.711 mu-law, one byte per sample, and the only codec
// carried on the wire.
const CodecMulaw CodecName = "mulaw"

const (
	// HeaderBytes is the size of the binary stream header a TCP source sends
	// once, before any audio.
	HeaderBytes = 12

	headerMagic = "SB01"

	minSampleRate = 4000
	maxSampleRate = 48000
)

var codecIds = map[CodecName]byte{
	CodecMulaw: 1,
}

// Format describes a stream well enough for a receiver to decode it without
// any out-of-band agreement. Every source declares one before sending audio.
type Format struct {
	Codec        CodecName `json:"codec"`
	SampleRate   int       `json:"sampleRate"`
	Channels     int       `json:"channels"`
	FrameSamples int       `json:"frameSamples"`
}

// Validate reports whether the relay can carry this format.
func (f Format) Validate() error {
	if _, ok := codecIds[f.Codec]; !ok {
		return fmt.Errorf("unknown codec %q", f.Codec)
	}

	if f.Channels != 1 {
		return fmt.Errorf("channels must be 1, got %d", f.Channels)
	}

	if f.SampleRate < minSampleRate || f.SampleRate > maxSampleRate {
		return fmt.Errorf("sample rate %d outside [%d,%d]", f.SampleRate, minSampleRate, maxSampleRate)
	}

	if f.FrameSamples < 1 || f.FrameSamples > 65535 {
		return fmt.Errorf("frame samples %d outside [1,65535]", f.FrameSamples)
	}

	return nil
}

// FrameBytes is the encoded size of one frame in this format. mu-law is one
// byte per sample.
func (f Format) FrameBytes() int {
	return f.FrameSamples * f.Channels
}

// FrameDuration is the wall-clock audio contained in one frame.
func (f Format) FrameDuration() time.Duration {
	if f.SampleRate <= 0 {
		return 0
	}

	return time.Duration(f.FrameSamples) * time.Second / time.Duration(f.SampleRate)
}

// BuildHeader returns the HeaderBytes-long binary header for this format.
func (f Format) BuildHeader() []byte {
	header := make([]byte, HeaderBytes)
	copy(header, headerMagic)
	header[4] = codecIds[f.Codec]
	header[5] = byte(f.Channels)
	binary.LittleEndian.PutUint16(header[6:], uint16(f.FrameSamples))
	binary.LittleEndian.PutUint32(header[8:], uint32(f.SampleRate))

	return header
}

func (f Format) String() string {
	return fmt.Sprintf("%s %d Hz mono %d samples/frame (%v)",
		f.Codec, f.SampleRate, f.FrameSamples, f.FrameDuration())
}

// ParseHeader reads a binary stream header. It returns an error when the magic
// does not match, which is the common case of a client speaking the old
// headerless protocol.
func ParseHeader(header []byte) (Format, error) {
	if len(header) < HeaderBytes {
		return Format{}, fmt.Errorf("header needs %d bytes, got %d", HeaderBytes, len(header))
	}

	if string(header[:4]) != headerMagic {
		return Format{}, errors.New("bad magic: not a sonic-bridge stream header")
	}

	format := Format{
		SampleRate:   int(binary.LittleEndian.Uint32(header[8:])),
		Channels:     int(header[5]),
		FrameSamples: int(binary.LittleEndian.Uint16(header[6:])),
	}

	for name, id := range codecIds {
		if id == header[4] {
			format.Codec = name
		}
	}

	if format.Codec == "" {
		return Format{}, fmt.Errorf("unknown codec id %d", header[4])
	}

	return format, format.Validate()
}
