package foxfire

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
)

// The bridge terminates TLS with a certificate it generated for itself at
// manufacture. Its Common Name is the 16-hex-character bridge ID, lowercased.
// Since we connect by IP address, the standard hostname verification path is
// useless to us: the certificate has no matching SAN. So we disable the
// default verification and substitute our own, which checks two things:
//
//  1. the leaf's Common Name equals the bridge ID we expect, and
//  2. if a root pool was supplied, the chain validates against it.
//
// Condition 1 alone is not authentication -- anyone can mint a certificate
// with an arbitrary CN. It only pins identity across reconnects. Supply the
// Signify root CA via WithRootCA for a real trust anchor. The bring-up path
// is: pair once on a network you trust, record the certificate fingerprint,
// and pin that.

// bridgeVerifier returns a VerifyPeerCertificate function enforcing the rules
// above. Either bridgeID or roots may be empty, but not both, or the returned
// verifier is a no-op and we refuse to build it.
func bridgeVerifier(bridgeID string, roots *x509.CertPool, fingerprint []byte) func([][]byte, [][]*x509.Certificate) error {
	want := strings.ToLower(strings.TrimSpace(bridgeID))

	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("%w: peer presented no certificate", ErrBridgeIdentity)
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("%w: parsing leaf: %v", ErrBridgeIdentity, err)
		}

		if len(fingerprint) > 0 {
			if got := sha256Sum(rawCerts[0]); !equalConstantTime(got, fingerprint) {
				return fmt.Errorf("%w: certificate fingerprint changed", ErrBridgeIdentity)
			}
			// A pinned fingerprint is the strongest statement available and
			// subsumes the weaker checks below.
			return nil
		}

		if want != "" {
			got := strings.ToLower(strings.TrimSpace(leaf.Subject.CommonName))
			if got != want {
				return fmt.Errorf("%w: certificate CN is %q, expected %q",
					ErrBridgeIdentity, leaf.Subject.CommonName, bridgeID)
			}
		}

		if roots != nil {
			intermediates := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				if c, err := x509.ParseCertificate(raw); err == nil {
					intermediates.AddCert(c)
				}
			}
			if _, err := leaf.Verify(x509.VerifyOptions{
				Roots:         roots,
				Intermediates: intermediates,
				// Deliberately no DNSName: we connect by IP and the bridge
				// certificate carries no matching SAN.
			}); err != nil {
				return fmt.Errorf("%w: chain verification failed: %v", ErrBridgeIdentity, err)
			}
		}

		return nil
	}
}

// PeerFingerprint dials the bridge, records the SHA-256 of its leaf
// certificate, and returns it. Intended to be run once, interactively, on a
// network the operator trusts, so that the result can be stored and passed to
// WithPinnedFingerprint thereafter. This is trust-on-first-use and it should
// be an explicit, visible act rather than a silent default.
func PeerFingerprint(addr string) ([]byte, error) {
	conn, err := tls.Dial("tcp", withPort(addr, "443"), &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // the entire point of this function
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("foxfire: dialing %s: %w", addr, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("%w: peer presented no certificate", ErrBridgeIdentity)
	}
	return sha256Sum(certs[0].Raw), nil
}
