package audio

import (
	"math"
	"testing"
)

func TestPcm16RoundTripIsExact(t *testing.T) {
	samples := []int16{0, 1, -1, 32767, -32768, 1234, -4321}

	decoded := DecodePcm16(EncodePcm16(samples))

	if len(decoded) != len(samples) {
		t.Fatalf("got %d samples back, want %d", len(decoded), len(samples))
	}

	for i := range samples {
		if decoded[i] != samples[i] {
			t.Fatalf("sample %d: got %d, want %d", i, decoded[i], samples[i])
		}
	}
}

func TestPcm16IsLittleEndian(t *testing.T) {
	encoded := EncodePcm16([]int16{0x0102})

	if encoded[0] != 0x02 || encoded[1] != 0x01 {
		t.Fatalf("got % x, want 02 01", encoded)
	}
}

// The G.711 standard pins these pairs. They are the anchors that prove the
// segment table and bias are applied in the right order.
func TestMulawMatchesStandardAnchors(t *testing.T) {
	cases := []struct {
		sample  int16
		encoded byte
	}{
		{0, 0xFF},
		{32124, 0x80},
		{-32124, 0x00},
	}

	for _, want := range cases {
		got := EncodeMulaw([]int16{want.sample})[0]
		if got != want.encoded {
			t.Errorf("EncodeMulaw(%d): got %#02x, want %#02x", want.sample, got, want.encoded)
		}

		back := DecodeMulaw([]byte{want.encoded})[0]
		if back != want.sample {
			t.Errorf("DecodeMulaw(%#02x): got %d, want %d", want.encoded, back, want.sample)
		}
	}
}

// Every one of the 256 codes must survive decode then encode unchanged, with
// one standard exception: G.711 has both a positive and a negative zero, and
// 0x7F (negative zero) re-encodes to 0xFF. A codec that fails this test is
// silently losing levels.
func TestMulawDecodeEncodeIsIdentityOverAllCodes(t *testing.T) {
	const negativeZero = 0x7F

	for code := 0; code < 256; code++ {
		want := byte(code)
		if code == negativeZero {
			want = 0xFF
		}

		sample := DecodeMulaw([]byte{byte(code)})[0]
		got := EncodeMulaw([]int16{sample})[0]

		if got != want {
			t.Fatalf("code %#02x decoded to %d and re-encoded to %#02x, want %#02x", code, sample, got, want)
		}
	}
}

func TestMulawQuantisationErrorStaysWithinStandardBound(t *testing.T) {
	var worstRelative float64

	for sample := -32768; sample <= 32767; sample++ {
		decoded := float64(DecodeMulaw(EncodeMulaw([]int16{int16(sample)}))[0])
		absolute := math.Abs(decoded - float64(sample))

		relative := absolute / math.Max(math.Abs(float64(sample)), 1024)
		if relative > worstRelative {
			worstRelative = relative
		}
	}

	if worstRelative > 0.09 {
		t.Fatalf("worst relative error %.4f exceeds the 9%% mu-law bound", worstRelative)
	}
}

func TestMulawHalvesTheBitrate(t *testing.T) {
	samples := make([]int16, 160)

	if got := len(EncodeMulaw(samples)); got != 160 {
		t.Fatalf("mu-law encoded %d bytes for 160 samples, want 160", got)
	}

	if got := len(EncodePcm16(samples)); got != 320 {
		t.Fatalf("pcm16 encoded %d bytes for 160 samples, want 320", got)
	}
}
