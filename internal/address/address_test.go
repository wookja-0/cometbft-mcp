package address

import (
	"encoding/hex"
	"testing"
)

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
		enc, _ := EncodeBech32(hrp, raw)
		got, err := ParseConsensus(enc)
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
		enc, _ := EncodeBech32(hrp, raw)
		if _, err := ParseConsensus(enc); err == nil {
			t.Errorf("%s: expected error", hrp)
		}
	}
}

func TestConsensusFromPubKey(t *testing.T) {
	// Public key / address pair from CometBFT's internal/test/config.go.
	got, err := ConsensusFromPubKey("/cosmos.crypto.ed25519.PubKey", "AT/+aaL1eB0477Mud9JMm8Sh8BIvOYlPGC9KkIUmFaE=")
	if err != nil || got != "A3258DCBF45DCA0DF052981870F2D1441A36D145" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ConsensusFromPubKey("/cosmos.crypto.secp256k1.PubKey", "AAAA"); err == nil {
		t.Fatal("expected unsupported key type")
	}
}

func TestEncodeValcons(t *testing.T) {
	got, err := EncodeBech32("cosmosvalcons", mustHex("89A28424AF509FE12A7EDF335CCE521F05C8C154"))
	if err != nil || got != "cosmosvalcons13x3ggf902z07z2n7mue4enjjruzu3s25368anl" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
