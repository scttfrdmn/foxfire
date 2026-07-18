package foxfire

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Credentials are what pairing yields. ClientKey is only needed for the
// Entertainment API; keep it anyway, since re-pairing to get it later means
// pressing the button again.
type Credentials struct {
	ApplicationKey string `json:"username"`
	ClientKey      string `json:"clientkey"`
}

// Pair performs the link-button handshake and returns credentials.
//
// The endpoint is /api, not /clip/v2 -- pairing is the one operation that
// still lives on the v1 path, because a client without a key cannot
// authenticate to v2 to ask for one.
//
// appName and instanceName are recorded on the bridge and shown to the user
// in the Hue app's list of connected apps. Make them honest; "foxfire" and
// the machine hostname are good choices.
//
// The button must have been pressed within the preceding 30 seconds. Use
// PairWait if you would rather poll than coordinate the timing yourself.
func Pair(ctx context.Context, addr, appName, instanceName string, opts ...Option) (Credentials, error) {
	cfg := config{timeout: defaultTimeout}
	for _, o := range opts {
		o(&cfg)
	}

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // see bridgeVerifier
	}
	if !cfg.insecure {
		if cfg.bridgeID == "" && cfg.roots == nil && len(cfg.fingerprint) == 0 {
			return Credentials{}, fmt.Errorf(
				"foxfire: no TLS trust configured for pairing; supply WithBridgeID " +
					"(from discovery) or explicitly WithInsecureTLS")
		}
		tlsCfg.VerifyPeerCertificate = bridgeVerifier(cfg.bridgeID, cfg.roots, cfg.fingerprint)
	}

	hc := &http.Client{
		Timeout:   cfg.timeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}

	body, err := json.Marshal(map[string]any{
		// The bridge requires the "app#instance" convention here and will
		// reject a devicetype without the separator.
		"devicetype":        fmt.Sprintf("%s#%s", appName, instanceName),
		"generateclientkey": true,
	})
	if err != nil {
		return Credentials{}, err
	}

	url := "https://" + withPort(addr, "443") + "/api"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("foxfire: pairing request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Credentials{}, err
	}

	// The v1 endpoint returns an array of single-key objects, each holding
	// either "success" or "error". It is an awkward shape and it is the last
	// place in the library where we have to deal with it.
	var results []struct {
		Success *Credentials `json:"success"`
		Error   *struct {
			Type        int    `json:"type"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &results); err != nil {
		return Credentials{}, fmt.Errorf("foxfire: decoding pairing response: %w", err)
	}
	if len(results) == 0 {
		return Credentials{}, fmt.Errorf("foxfire: empty pairing response")
	}

	r := results[0]
	if r.Error != nil {
		// Type 101 is the documented "link button not pressed" code.
		if r.Error.Type == 101 || strings.Contains(strings.ToLower(r.Error.Description), "link button") {
			return Credentials{}, ErrLinkButtonNotPressed
		}
		return Credentials{}, fmt.Errorf("foxfire: pairing rejected: %s", r.Error.Description)
	}
	if r.Success == nil || r.Success.ApplicationKey == "" {
		return Credentials{}, fmt.Errorf("foxfire: pairing returned no key")
	}
	return *r.Success, nil
}

// PairWait polls Pair until the button is pressed or the context is done.
// The caller is expected to have told a human to go press it.
func PairWait(ctx context.Context, addr, appName, instanceName string, opts ...Option) (Credentials, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		creds, err := Pair(ctx, addr, appName, instanceName, opts...)
		if err == nil {
			return creds, nil
		}
		if !isLinkButtonError(err) {
			return Credentials{}, err
		}
		select {
		case <-ctx.Done():
			return Credentials{}, fmt.Errorf("foxfire: gave up waiting for link button: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func isLinkButtonError(err error) bool {
	return err == ErrLinkButtonNotPressed
}
