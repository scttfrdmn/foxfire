package foxfire

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"strings"
)

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func equalConstantTime(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// withPort appends a default port if addr does not already carry one. IPv6
// literals are bracketed as needed.
func withPort(addr, defaultPort string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	if strings.Contains(addr, ":") { // bare IPv6 literal
		return "[" + addr + "]:" + defaultPort
	}
	return addr + ":" + defaultPort
}

// Pointer helpers. Update structs use pointers throughout so that "absent"
// and "set to zero" are distinguishable, which matters because the bridge
// applies partial updates: sending brightness 0 is a command to dim to zero,
// while omitting brightness leaves it untouched.

func Bool(v bool) *bool        { return &v }
func Float(v float64) *float64 { return &v }
func Int(v int) *int           { return &v }
func String(v string) *string  { return &v }
func Duration(ms int) *int     { return &ms }
