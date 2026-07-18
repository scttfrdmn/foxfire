package foxfire

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const (
	// The bridge's documented ceilings. Exceeding them does not produce an
	// error; commands are simply dropped, which is worse. Grouped-light
	// commands fan out to every member over Zigbee and are correspondingly
	// more expensive.
	defaultLightRate = 10.0
	defaultGroupRate = 1.0

	defaultTimeout = 10 * time.Second
)

// Client talks to a single bridge.
type Client struct {
	addr   string // host or host:port, no scheme
	appKey string
	http   *http.Client

	lightLim *rate.Limiter
	groupLim *rate.Limiter

	// Typed resource accessors. These are thin structs holding a back
	// pointer; they exist to give callers c.Lights.Get rather than a flat
	// namespace of forty methods.
	Lights        *LightService
	GroupedLights *GroupedLightService
	Rooms         *RoomService
	Zones         *ZoneService
	Scenes        *SceneService
	Devices       *DeviceService
	Motion        *MotionService
	Temperature   *TemperatureService
}

// Option configures a Client.
type Option func(*config)

type config struct {
	bridgeID    string
	roots       *x509.CertPool
	fingerprint []byte
	insecure    bool
	timeout     time.Duration
	lightRate   float64
	groupRate   float64
	transport   http.RoundTripper
}

// WithBridgeID pins the expected Common Name of the bridge certificate. This
// is the 16-hex-character ID reported by discovery.
func WithBridgeID(id string) Option {
	return func(c *config) { c.bridgeID = id }
}

// WithRootCA supplies a trust anchor for the bridge certificate chain --
// in practice the Signify root published on the developer portal.
func WithRootCA(pool *x509.CertPool) Option {
	return func(c *config) { c.roots = pool }
}

// WithPinnedFingerprint pins the SHA-256 of the bridge's leaf certificate,
// as returned by PeerFingerprint. This is the strongest option available
// without a chain to a public root, and it supersedes WithBridgeID.
func WithPinnedFingerprint(sum []byte) Option {
	return func(c *config) { c.fingerprint = sum }
}

// WithInsecureTLS disables all certificate verification. It exists for
// bring-up and for tests. It is not a default and it never will be.
func WithInsecureTLS() Option {
	return func(c *config) { c.insecure = true }
}

// WithTimeout sets the per-request timeout. It does not apply to the event
// stream, which is long-lived by construction.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithRateLimits overrides the default command ceilings. Raising them is
// usually a mistake; lowering them is reasonable on a busy Zigbee mesh.
func WithRateLimits(lightPerSec, groupPerSec float64) Option {
	return func(c *config) {
		c.lightRate = lightPerSec
		c.groupRate = groupPerSec
	}
}

// WithTransport substitutes the underlying RoundTripper. Intended for tests
// and for callers who need proxy or dial control. Supplying this bypasses
// the TLS options entirely; you own verification at that point.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *config) { c.transport = rt }
}

// New builds a client for the bridge at addr using the supplied application
// key. addr is a host or IP, optionally with a port; the scheme is always
// https and is not accepted as part of addr.
//
// At least one of WithBridgeID, WithRootCA, WithPinnedFingerprint, or
// WithInsecureTLS must be supplied, so that the security posture is always a
// deliberate choice rather than a default someone inherited.
func New(addr, appKey string, opts ...Option) (*Client, error) {
	if addr == "" {
		return nil, errors.New("foxfire: empty bridge address")
	}
	if appKey == "" {
		return nil, errors.New("foxfire: empty application key; use Pair to obtain one")
	}

	cfg := config{
		timeout:   defaultTimeout,
		lightRate: defaultLightRate,
		groupRate: defaultGroupRate,
	}
	for _, o := range opts {
		o(&cfg)
	}

	transport := cfg.transport
	if transport == nil {
		if !cfg.insecure && cfg.bridgeID == "" && cfg.roots == nil && len(cfg.fingerprint) == 0 {
			return nil, errors.New(
				"foxfire: no TLS trust configured; supply WithBridgeID, WithRootCA, " +
					"WithPinnedFingerprint, or explicitly WithInsecureTLS")
		}
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Always true: the bridge certificate has no SAN matching the IP
			// we dial, so the stock verifier can never succeed. Real checking
			// happens in VerifyPeerCertificate below.
			InsecureSkipVerify: true, //nolint:gosec // see bridgeVerifier
		}
		if !cfg.insecure {
			tlsCfg.VerifyPeerCertificate = bridgeVerifier(cfg.bridgeID, cfg.roots, cfg.fingerprint)
		}
		transport = &http.Transport{
			TLSClientConfig:     tlsCfg,
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
	}

	c := &Client{
		addr:   withPort(addr, "443"),
		appKey: appKey,
		http: &http.Client{
			Transport: transport,
			Timeout:   cfg.timeout,
		},
		lightLim: rate.NewLimiter(rate.Limit(cfg.lightRate), 2),
		groupLim: rate.NewLimiter(rate.Limit(cfg.groupRate), 1),
	}

	c.Lights = &LightService{c: c}
	c.GroupedLights = &GroupedLightService{c: c}
	c.Rooms = &RoomService{c: c}
	c.Zones = &ZoneService{c: c}
	c.Scenes = &SceneService{c: c}
	c.Devices = &DeviceService{c: c}
	c.Motion = &MotionService{c: c}
	c.Temperature = &TemperatureService{c: c}

	return c, nil
}

// Addr reports the bridge address the client is bound to.
func (c *Client) Addr() string { return c.addr }
