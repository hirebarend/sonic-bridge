package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// These digests pin the Go codec to the frozen G.711 wire bytes across its
// whole input domain. They run inside `go test`, so they hold for a
// contributor who has neither Node nor a C++ compiler.
//
// They say nothing about the other two implementations. That is what
// scripts/check-wire-format.sh is for: it proves the firmware encodes what Go
// encodes and the browser decodes what Go decodes.
const (
	mulawEncodeDigest = "81d633c9e6972a18c74a58720b96cb8ca0bdd096d4060b646dd708c3b846019a"
	mulawDecodeDigest = "3dab54339e520bb2c924826e3b72a917a2b612e9fd12fc867500f1d983a75827"
)

func TestMulawMatchesTheBrowserImplementation(t *testing.T) {
	everySample := make([]int16, 65536)
	for i := range everySample {
		everySample[i] = int16(i - 32768)
	}

	everyCode := make([]byte, 256)
	for i := range everyCode {
		everyCode[i] = byte(i)
	}

	if got := digest(EncodeMulaw(everySample)); got != mulawEncodeDigest {
		t.Errorf("encode digest over all 65536 samples:\n got  %s\n want %s", got, mulawEncodeDigest)
	}

	if got := digest(EncodePcm16(DecodeMulaw(everyCode))); got != mulawDecodeDigest {
		t.Errorf("decode digest over all 256 codes:\n got  %s\n want %s", got, mulawDecodeDigest)
	}
}

func digest(payload []byte) string {
	sum := sha256.Sum256(payload)

	return hex.EncodeToString(sum[:])
}
