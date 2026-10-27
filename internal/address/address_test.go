package address

import (
	"encoding/hex"
	"strings"
	"testing"
)

// encodeBech32 is the inverse of decodeBech32, used to build test vectors.
func encodeBech32(hrp string, data []byte) string {
	values, _ := convertBits(data, 8, 5, true)
	pm := polymod(append(append(expandHRP(hrp), values...), 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString(hrp + "1")
	for _, v := range values {
		b.WriteByte(charset[v])
	}
	for i := range 6 {
		b.WriteByte(charset[(pm>>uint(5*(5-i)))&31])
	}
	return b.String()
}

func TestBIP173ValidChecksums(t *testing.T) {
	for _, s := range []string{
		"A12UEL5L",
		"abcdef1qpzry9x8gf2tvdw0s3jn54khce6mua7lmqqqxw",
		"split1checkupstagehandshakeupstreamerranterredcaperred2y9e3w",
	} {
		if _, _, err := decodeBech32(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestBIP173InvalidStrings(t *testing.T) {
	for _, s := range []string{
		"pzry9x0s0muk",  // no separator
		"1pzry9x0s0muk", // empty prefix
		"x1b4n0q5v",     // invalid data character
		"li1dgmt3",      // checksum too short
		"A1G7SGD8",      // checksum computed with uppercase prefix
		"a12UEL5L",      // mixed case
		"abcdef1qpzry9x8gf2tvdw0s3jn54khce6mua7lmqqqxx", // bad checksum
	} {
		if _, _, err := decodeBech32(s); err == nil {
			t.Errorf("%s: expected error", s)
		}
	}
}

func TestParseConsensusHex(t *testing.T) {
	got, err := ParseConsensus("  a1b2c3d4e5f60718293a4b5c6d7e8f9012345678 ")
	if err != nil || got != "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestParseConsensusBech32RoundTrip(t *testing.T) {
	raw, _ := hex.DecodeString("A1B2C3D4E5F60718293A4B5C6D7E8F9012345678")
	for _, hrp := range []string{"cosmosvalcons", "osmovalcons"} {
		got, err := ParseConsensus(encodeBech32(hrp, raw))
		if err != nil || got != "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678" {
			t.Fatalf("%s: got %q, %v", hrp, got, err)
		}
	}
}

func TestParseConsensusMatchesCometBFT(t *testing.T) {
	// From the localnet: `simd comet show-address` vs /status validator_info.address.
	got, err := ParseConsensus("cosmosvalcons13x3ggf902z07z2n7mue4enjjruzu3s25368anl")
	if err != nil || got != "89A28424AF509FE12A7EDF335CCE521F05C8C154" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestParseConsensusRejectsOtherPrefixes(t *testing.T) {
	raw := make([]byte, 20)
	for _, hrp := range []string{"cosmos", "cosmosvaloper", "cosmosvalconspub"} {
		if _, err := ParseConsensus(encodeBech32(hrp, raw)); err == nil {
			t.Errorf("%s: expected error", hrp)
		}
	}
}
