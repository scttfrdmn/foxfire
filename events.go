package foxfire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/scttfrdmn/foxfire/internal/sse"
)

const eventStreamPath = "/eventstream/clip/v2"

// Event is one update delta from the bridge.
//
// Deltas are sparse: a brightness change arrives with only id, type, owner,
// and dimming populated. Everything else is nil. This is a feature -- it is
// what makes the stream cheap -- but it means an Event is not a snapshot and
// must not be treated as one. Use the foxfire/state package if you need
// current state rather than changes.
type Event struct {
	ID    ID           `json:"id"`
	IDv1  string       `json:"id_v1,omitempty"`
	Type  ResourceType `json:"type"`
	Owner *Ref         `json:"owner,omitempty"`

	On               *On                `json:"on,omitempty"`
	Dimming          *Dimming           `json:"dimming,omitempty"`
	Color            *Color             `json:"color,omitempty"`
	ColorTemperature *ColorTemperature  `json:"color_temperature,omitempty"`
	Motion           *MotionReport      `json:"motion,omitempty"`
	Temperature      *TemperatureReport `json:"temperature,omitempty"`
	Button           *ButtonReport      `json:"button,omitempty"`

	// Raw is the undecoded object, so that resource types this library does
	// not model yet are still usable rather than silently dropped.
	Raw json.RawMessage `json:"-"`
}

type MotionReport struct {
	Motion      bool `json:"motion"`
	MotionValid bool `json:"motion_valid"`
}

type TemperatureReport struct {
	Temperature      float64 `json:"temperature"`
	TemperatureValid bool    `json:"temperature_valid"`
}

type ButtonReport struct {
	LastEvent string `json:"last_event"` // initial_press, repeat, short_release, long_release
}

// EventBatch is one dispatched frame. The bridge coalesces changes that occur
// close together, so a scene recall arrives as a single batch containing every
// affected light rather than as N frames. Consumers that redraw UI should key
// off the batch, not the individual event, or they will render intermediate
// states nobody asked to see.
type EventBatch struct {
	CreationTime time.Time
	Type         string // "update", "add", "delete", "error"
	Events       []Event
}

// Subscribe opens the event stream and returns a channel of batches. The
// channel is closed when ctx is done or when the stream fails permanently.
//
// Transient failures -- bridge reboot, wifi blip, cable pulled -- are handled
// internally with exponential backoff and jitter, and are reported on the
// returned error channel without terminating the subscription. A caller that
// only wants lights to work can ignore the error channel entirely; a caller
// running a daemon should log from it.
func (c *Client) Subscribe(ctx context.Context) (<-chan EventBatch, <-chan error) {
	batches := make(chan EventBatch, 16)
	errs := make(chan error, 8)

	go func() {
		defer close(batches)
		defer close(errs)

		var attempt int
		for {
			if ctx.Err() != nil {
				return
			}

			err := c.streamOnce(ctx, batches)
			if ctx.Err() != nil {
				return
			}

			// A clean EOF is not an error condition: the bridge closes idle
			// streams periodically and expects the client to come back.
			if err != nil && !errors.Is(err, io.EOF) {
				select {
				case errs <- err:
				default: // never block the reconnect loop on a slow consumer
				}
			}

			// An auth failure will not fix itself. Anything else might.
			if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrBridgeIdentity) {
				return
			}

			delay := backoff(attempt)
			attempt++
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()

	return batches, errs
}

// streamOnce holds one connection open until it fails.
func (c *Client) streamOnce(ctx context.Context, out chan<- EventBatch) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+c.addr+eventStreamPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set(keyHeader, c.appKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// The client-wide timeout would guillotine a long-lived stream, so this
	// request gets a transport-only client with no deadline. Liveness is
	// instead the responsibility of ctx and of the bridge's own keepalives.
	streamClient := &http.Client{Transport: c.http.Transport}

	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &APIError{StatusCode: resp.StatusCode}
	}

	r := sse.NewReader(resp.Body)
	for {
		frame, err := r.Next()
		if err != nil {
			return err
		}

		var raw []struct {
			CreationTime time.Time         `json:"creationtime"`
			Type         string            `json:"type"`
			Data         []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(frame.Data, &raw); err != nil {
			// A frame we cannot parse should not kill the stream; skip it.
			continue
		}

		for _, b := range raw {
			batch := EventBatch{
				CreationTime: b.CreationTime,
				Type:         b.Type,
				Events:       make([]Event, 0, len(b.Data)),
			}
			for _, item := range b.Data {
				var ev Event
				if err := json.Unmarshal(item, &ev); err != nil {
					continue
				}
				ev.Raw = item
				batch.Events = append(batch.Events, ev)
			}
			select {
			case out <- batch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// backoff returns a jittered exponential delay capped at 30s. The jitter
// matters: a household with several subscribers reconnecting after a bridge
// reboot should not synchronize into a thundering herd against a device with
// a single-core CPU.
func backoff(attempt int) time.Duration {
	const base = 500 * time.Millisecond
	const max = 30 * time.Second

	d := time.Duration(float64(base) * math.Pow(2, float64(min(attempt, 6))))
	if d > max {
		d = max
	}
	jitter := time.Duration(rand.Int63n(int64(d/2 + 1)))
	return d/2 + jitter
}
