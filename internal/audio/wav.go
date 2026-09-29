package audio

import "encoding/binary"

// WavHeaderBytes is the size of the canonical 44-byte RIFF/WAVE header.
const WavHeaderBytes = 44

// BuildLiveWavHeader returns a RIFF/WAVE header for an endless 16-bit PCM
// stream. Players that honour chunked transfer encoding keep reading past the
// declared size.
//
// The declared size is the largest that keeps both size fields inside positive
// signed 32-bit range. Plenty of parsers read RIFF chunk sizes as int32, where
// anything above 0x7FFFFFFF is negative. Note the ordering trap: the RIFF size
// is the data size plus 36, so capping the data size at 0x7FFFFFFF would push
// the RIFF field negative instead of fixing it.
func BuildLiveWavHeader(sampleRate, channels int) []byte {
	const bitsPerSample = 16
	const dataBytes = 0x7FFFFFFF - WavHeaderBytes

	blockAlign := channels * bitsPerSample / 8
	header := make([]byte, WavHeaderBytes)

	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(dataBytes+WavHeaderBytes-8))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:], uint32(sampleRate*blockAlign))
	binary.LittleEndian.PutUint16(header[32:], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:], bitsPerSample)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataBytes))

	return header
}
