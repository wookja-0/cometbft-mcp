// Package address parses CometBFT consensus addresses given either as the
// 40-character hex form CometBFT RPC reports or as a Cosmos bech32 "valcons"
// address (cosmosvalcons1…, osmovalcons1…).
package address

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ParseConsensus returns the upper-case hex consensus address.
func ParseConsensus(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) == 40 {
		if _, err := hex.DecodeString(s); err == nil {
			return strings.ToUpper(s), nil
		}
	}
	hrp, data, err := decodeBech32(s)
	if err != nil {
		return "", fmt.Errorf("not a 40-char hex or bech32 address: %w", err)
	}
	if !strings.HasSuffix(hrp, "valcons") {
		return "", fmt.Errorf("bech32 prefix %q is not a consensus address (want …valcons)", hrp)
	}
	if len(data) != 20 {
		return "", fmt.Errorf("consensus address must be 20 bytes, got %d", len(data))
	}
	return strings.ToUpper(hex.EncodeToString(data)), nil
}

// EncodeBech32 encodes 8-bit data under the given human-readable prefix.
func EncodeBech32(hrp string, data []byte) (string, error) {
	values, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	pm := polymod(append(append(expandHRP(hrp), values...), 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString(hrp + "1")
	for _, v := range values {
		b.WriteByte(charset[v])
	}
	for i := range 6 {
		b.WriteByte(charset[(pm>>uint(5*(5-i)))&31])
	}
	return b.String(), nil
}

// ConsensusFromPubKey derives the hex consensus address from a staking
// module consensus_pubkey: the first 20 bytes of SHA-256 of an ed25519 key.
func ConsensusFromPubKey(typeURL, keyB64 string) (string, error) {
	if typeURL != "/cosmos.crypto.ed25519.PubKey" {
		return "", fmt.Errorf("unsupported consensus key type %q", typeURL)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid ed25519 public key")
	}
	sum := sha256.Sum256(key)
	return strings.ToUpper(hex.EncodeToString(sum[:20])), nil
}

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// decodeBech32 implements BIP-173 decoding and returns the 8-bit payload.
func decodeBech32(s string) (string, []byte, error) {
	if len(s) < 8 || len(s) > 1023 {
		return "", nil, errors.New("invalid length")
	}
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", nil, errors.New("mixed case")
	}
	s = strings.ToLower(s)
	sep := strings.LastIndexByte(s, '1')
	if sep < 1 || sep+7 > len(s) {
		return "", nil, errors.New("missing separator")
	}
	hrp := s[:sep]
	for _, c := range hrp {
		if c < 33 || c > 126 {
			return "", nil, errors.New("invalid prefix character")
		}
	}
	values := make([]byte, 0, len(s)-sep-1)
	for _, c := range s[sep+1:] {
		i := strings.IndexRune(charset, c)
		if i < 0 {
			return "", nil, fmt.Errorf("invalid character %q", c)
		}
		values = append(values, byte(i))
	}
	if polymod(append(expandHRP(hrp), values...)) != 1 {
		return "", nil, errors.New("invalid checksum")
	}
	data, err := convertBits(values[:len(values)-6], 5, 8, false)
	if err != nil {
		return "", nil, err
	}
	return hrp, data, nil
}

func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func expandHRP(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc, bits uint
	maxv := uint(1)<<to - 1
	var out []byte
	for _, v := range data {
		acc = acc<<from | uint(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxv))
		}
	} else if bits >= from || acc<<(to-bits)&maxv != 0 {
		return nil, errors.New("invalid padding")
	}
	return out, nil
}
