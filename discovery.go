package foxfire

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	mdnsService  = "_hue._tcp"
	mdnsDomain   = "local."
	cloudDiscURL = "https://discovery.meethue.com/"
)

// Bridge is a discovered bridge.
type Bridge struct {
	ID     string // 16 hex characters; also the certificate Common Name
	Addr   string // IP address, no port
	Port   int
	Name   string
	Source string // "mdns" or "cloud"
}

// Discover finds bridges on the local network. It tries mDNS first, because
// it works on networks with no outbound internet and does not tell Signify
// that you are looking. If mDNS yields nothing before the context expires or
// the timeout elapses, it falls back to the cloud discovery endpoint, which
// returns bridges whose most recent outbound connection came from the
// caller's public IP.
//
// The cloud fallback can be suppressed by passing a context that is already
// short, or by calling DiscoverMDNS directly.
func Discover(ctx context.Context, timeout time.Duration) ([]Bridge, error) {
	mdnsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bridges, err := DiscoverMDNS(mdnsCtx)
	if err == nil && len(bridges) > 0 {
		return bridges, nil
	}

	// mDNS is routinely blocked on segmented home networks and on most
	// corporate wifi, so a failure here is expected rather than exceptional.
	cloudBridges, cloudErr := DiscoverCloud(ctx)
	if cloudErr != nil {
		if err != nil {
			return nil, fmt.Errorf("foxfire: mdns failed (%v) and cloud discovery failed: %w", err, cloudErr)
		}
		return nil, fmt.Errorf("foxfire: cloud discovery failed: %w", cloudErr)
	}
	if len(cloudBridges) == 0 {
		return nil, ErrNoBridges
	}
	return cloudBridges, nil
}

// DiscoverMDNS browses for _hue._tcp on the local link.
func DiscoverMDNS(ctx context.Context) ([]Bridge, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("foxfire: mdns resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 8)
	if err := resolver.Browse(ctx, mdnsService, mdnsDomain, entries); err != nil {
		return nil, fmt.Errorf("foxfire: mdns browse: %w", err)
	}

	var out []Bridge
	seen := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			if len(out) == 0 {
				return nil, ErrNoBridges
			}
			return out, nil
		case e, ok := <-entries:
			if !ok {
				if len(out) == 0 {
					return nil, ErrNoBridges
				}
				return out, nil
			}
			b := Bridge{
				Name:   e.Instance,
				Port:   e.Port,
				Source: "mdns",
			}
			if len(e.AddrIPv4) > 0 {
				b.Addr = e.AddrIPv4[0].String()
			} else if len(e.AddrIPv6) > 0 {
				b.Addr = e.AddrIPv6[0].String()
			}
			// The bridge advertises bridgeid in its TXT records. Without it
			// we cannot pin the certificate, so a record lacking one is
			// reported but flagged by an empty ID.
			for _, txt := range e.Text {
				if k, v, found := strings.Cut(txt, "="); found && strings.EqualFold(k, "bridgeid") {
					b.ID = strings.ToLower(v)
				}
			}
			if b.Addr == "" || seen[b.Addr] {
				continue
			}
			seen[b.Addr] = true
			out = append(out, b)
		}
	}
}

// DiscoverCloud queries Signify's discovery endpoint. It only returns bridges
// that share a public IP with the caller, so it is useless over a VPN and
// requires outbound internet.
func DiscoverCloud(ctx context.Context) ([]Bridge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudDiscURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("foxfire: cloud discovery returned HTTP %d", resp.StatusCode)
	}

	var raw []struct {
		ID   string `json:"id"`
		IP   string `json:"internalipaddress"`
		Port int    `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("foxfire: decoding cloud discovery: %w", err)
	}

	out := make([]Bridge, 0, len(raw))
	for _, r := range raw {
		port := r.Port
		if port == 0 {
			port = 443
		}
		out = append(out, Bridge{
			ID:     strings.ToLower(r.ID),
			Addr:   r.IP,
			Port:   port,
			Source: "cloud",
		})
	}
	return out, nil
}
