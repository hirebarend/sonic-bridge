package audio

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	RecordHeaderBytes      = 9
	RecordAudio       byte = 1
	RecordQuiet       byte = 2
	// JavaScript can represent these sample positions exactly (thousands of years).
	MaxPosition uint64 = 1<<53 - 1
)

// Record carries one frame, or the start/current position of intentional silence.
// Positions count captured samples, including suppressed samples, per connection.
type Record struct {
	Position uint64
	Frame    []byte
}

func (r Record) Quiet() bool { return len(r.Frame) == 0 }

func (f Format) BuildSparseHeader() []byte {
	h := f.BuildHeader()
	h[3] = '2'
	return h
}

func (r Record) Bytes() []byte {
	b := make([]byte, RecordHeaderBytes+len(r.Frame))
	b[0] = RecordAudio
	if r.Quiet() {
		b[0] = RecordQuiet
	}
	binary.LittleEndian.PutUint64(b[1:], r.Position)
	copy(b[RecordHeaderBytes:], r.Frame)
	return b
}

// ReadRecord reads fixed, bounded records; no peer-controlled allocation lengths.
func ReadRecord(reader io.Reader, f Format) (Record, error) {
	var h [RecordHeaderBytes]byte
	if _, err := io.ReadFull(reader, h[:]); err != nil {
		return Record{}, err
	}
	r := Record{Position: binary.LittleEndian.Uint64(h[1:])}
	if r.Position > MaxPosition-uint64(f.FrameSamples) || r.Position%uint64(f.FrameSamples) != 0 {
		return Record{}, fmt.Errorf("invalid sample position %d", r.Position)
	}
	switch h[0] {
	case RecordQuiet:
		return r, nil
	case RecordAudio:
		r.Frame = make([]byte, f.FrameBytes())
		_, err := io.ReadFull(reader, r.Frame)
		return r, err
	default:
		return Record{}, fmt.Errorf("unknown record type %d", h[0])
	}
}
