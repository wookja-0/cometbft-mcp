// Package sanitize prepares node data for an LLM: it wraps text an outsider
// controls, and hides network details unless the operator opted in.
package sanitize

import (
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	openTag  = "<untrusted>"
	closeTag = "</untrusted>"

	// MaxUntrustedRunes bounds a single wrapped string. Monikers are short
	// by convention; anything longer is either a mistake or an attack.
	MaxUntrustedRunes = 128
)

// Untrusted wraps text that a third party controls (monikers, version
// strings, upgrade names) so the model treats it as data, not instructions.
// Control characters are dropped, the text is truncated, and any embedded
// envelope tag is neutralized so the text cannot close the envelope early.
func Untrusted(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == MaxUntrustedRunes {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return openTag + neutralize(b.String()) + closeTag
}

// neutralize defangs anything that looks like our envelope tags,
// case-insensitively, by swapping '<' for a lookalike.
func neutralize(s string) string {
	lower := strings.ToLower(s)
	if !strings.Contains(lower, "<untrusted") && !strings.Contains(lower, "</untrusted") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '<' && (strings.HasPrefix(lower[i:], "<untrusted") || strings.HasPrefix(lower[i:], "</untrusted")) {
			b.WriteString("‹")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Network hides or reveals network identifiers according to the operator's
// --reveal-network choice.
type Network struct {
	Reveal bool
}

// IP describes an address by class only ("private-ipv4", "public-ipv6", …)
// unless revealing is enabled. The class is what matters for judging a
// sentry topology; the address itself is what an attacker wants.
func (n Network) IP(s string) string {
	if n.Reveal {
		if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
			return s
		}
		return Untrusted(s) // revealing is about addresses, not arbitrary node text
	}
	return IPClass(s)
}

// IPClass classifies an address without revealing it.
func IPClass(s string) string {
	addr, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap()
	family := "ipv6"
	if addr.Is4() {
		family = "ipv4"
	}
	switch {
	case addr.IsLoopback():
		return "loopback-" + family
	case addr.IsUnspecified():
		return "unspecified-" + family
	case addr.IsPrivate(), addr.IsLinkLocalUnicast(), isCGNAT(addr):
		return "private-" + family
	default:
		return "public-" + family
	}
}

// IsPublic reports whether s is a globally routable address.
func IsPublic(s string) bool {
	return strings.HasPrefix(IPClass(s), "public-")
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isCGNAT(a netip.Addr) bool { return a.Is4() && cgnat.Contains(a) }

// NodeID shortens a p2p node ID to a stable, non-dialable prefix.
func (n Network) NodeID(id string) string {
	if !nodeIDRe.MatchString(id) {
		return Untrusted(id)
	}
	if n.Reveal {
		return id
	}
	return id[:8] + "…"
}

// nodeIDRe matches a p2p node ID: hex, as CometBFT derives it from the node key.
var nodeIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8,64}$`)
var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]{0,15}$`)

// ListenAddr masks the host part of a listen address such as
// "tcp://0.0.0.0:26656", keeping scheme and port.
func (n Network) ListenAddr(addr string) string {
	scheme, rest, ok := strings.Cut(addr, "://")
	if !ok {
		scheme, rest = "", addr
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil || (scheme != "" && !schemeRe.MatchString(scheme)) {
		return "masked"
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "masked"
	}
	if n.Reveal {
		if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			return addr
		}
		return Untrusted(addr)
	}
	masked := IPClass(host) + ":" + port
	if scheme != "" {
		masked = scheme + "://" + masked
	}
	return masked
}
