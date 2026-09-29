package audio

// mu-law is the only codec on the wire. ITU-T G.711 mu-law is a logarithmic
// 8-bit encoding that halves the bitrate of linear 16-bit samples and costs
// roughly 38 dB of signal-to-noise ratio, which is inaudible for speech. The
// segment table and the +132 bias are fixed by the standard.
//
// The pcm16 helpers below are not a wire codec. They are the linear byte layout
// that WAV carries and that audio devices produce, kept here because two places
// need to convert between it and samples.

const (
	mulawBias = 132
	mulawClip = 8159
)

var mulawSegmentEnds = [8]int{0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF}

// EncodeMulaw compresses linear samples to one byte each.
func EncodeMulaw(samples []int16) []byte {
	encoded := make([]byte, len(samples))
	for i, sample := range samples {
		encoded[i] = encodeMulawSample(sample)
	}

	return encoded
}

// DecodeMulaw expands mu-law bytes back to linear samples.
func DecodeMulaw(encoded []byte) []int16 {
	samples := make([]int16, len(encoded))
	for i, value := range encoded {
		samples[i] = decodeMulawSample(value)
	}

	return samples
}

// EncodePcm16 writes samples as signed 16-bit little-endian bytes, which is
// what WAV carries.
func EncodePcm16(samples []int16) []byte {
	encoded := make([]byte, len(samples)*2)
	for i, sample := range samples {
		encoded[i*2] = byte(uint16(sample))
		encoded[i*2+1] = byte(uint16(sample) >> 8)
	}

	return encoded
}

// DecodePcm16 reads signed 16-bit little-endian bytes as samples, which is what
// a capture device hands back.
func DecodePcm16(encoded []byte) []int16 {
	samples := make([]int16, len(encoded)/2)
	for i := range samples {
		samples[i] = int16(uint16(encoded[i*2]) | uint16(encoded[i*2+1])<<8)
	}

	return samples
}

func encodeMulawSample(sample int16) byte {
	magnitude := int(sample) >> 2
	mask := 0xFF

	if magnitude < 0 {
		magnitude = -magnitude
		mask = 0x7F
	}

	if magnitude > mulawClip {
		magnitude = mulawClip
	}

	magnitude += mulawBias >> 2

	segment := findMulawSegment(magnitude)
	if segment >= len(mulawSegmentEnds) {
		return byte(0x7F ^ mask)
	}

	return byte(((segment << 4) | ((magnitude >> (segment + 1)) & 0x0F)) ^ mask)
}

func decodeMulawSample(value byte) int16 {
	inverted := int(^value)
	magnitude := ((inverted & 0x0F) << 3) + mulawBias
	magnitude <<= (inverted & 0x70) >> 4

	if inverted&0x80 != 0 {
		return int16(mulawBias - magnitude)
	}

	return int16(magnitude - mulawBias)
}

func findMulawSegment(magnitude int) int {
	for segment, end := range mulawSegmentEnds {
		if magnitude <= end {
			return segment
		}
	}

	return len(mulawSegmentEnds)
}
