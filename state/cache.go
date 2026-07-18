// Package state maintains a current-state view of a bridge by seeding from a
// full GET and then folding in event deltas.
//
// It is deliberately a separate package. The event stream is useful on its
// own -- a motion-triggered script wants edges, not levels -- and callers who
// only want edges should not carry a cache they never read. Equally, callers
// who want levels should not have to write the reconciliation themselves,
// because the sparse-delta semantics are easy to get subtly wrong.
package state

import (
	"context"
	"sync"
	"time"

	"github.com/scttfrdmn/foxfire"
)

// Cache is a concurrency-safe snapshot of light state.
type Cache struct {
	mu     sync.RWMutex
	lights map[foxfire.ID]foxfire.Light
	rooms  map[foxfire.ID]foxfire.Room

	lastEvent time.Time
	seeded    bool
}

func New() *Cache {
	return &Cache{
		lights: make(map[foxfire.ID]foxfire.Light),
		rooms:  make(map[foxfire.ID]foxfire.Room),
	}
}

// Seed populates the cache from a full read. This must happen before folding
// deltas: an event tells you what changed, not what things are, so a cache
// built from events alone is missing every light that has not moved since the
// process started.
func (c *Cache) Seed(ctx context.Context, cl *foxfire.Client) error {
	lights, err := cl.Lights.List(ctx)
	if err != nil {
		return err
	}
	rooms, err := cl.Rooms.List(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range lights {
		c.lights[l.ID] = l
	}
	for _, r := range rooms {
		c.rooms[r.ID] = r
	}
	c.seeded = true
	return nil
}

// Apply folds one batch of deltas into the cache. Unknown IDs are inserted
// rather than dropped, so a light paired while the process is running shows
// up without a re-seed, albeit with only the fields the event carried.
func (c *Cache) Apply(batch foxfire.EventBatch) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastEvent = batch.CreationTime

	for _, ev := range batch.Events {
		if ev.Type != foxfire.TypeLight {
			continue
		}
		switch batch.Type {
		case "delete":
			delete(c.lights, ev.ID)
			continue
		}

		l := c.lights[ev.ID]
		l.ID = ev.ID
		if ev.Owner != nil {
			l.Owner = *ev.Owner
		}
		// Each field is overwritten only when present. This is the whole
		// point of the exercise: a nil Dimming on an on/off event means
		// "brightness unchanged", not "brightness zero".
		if ev.On != nil {
			l.On = *ev.On
		}
		if ev.Dimming != nil {
			if l.Dimming == nil {
				l.Dimming = &foxfire.Dimming{}
			}
			l.Dimming.Brightness = ev.Dimming.Brightness
		}
		if ev.Color != nil {
			if l.Color == nil {
				l.Color = &foxfire.Color{}
			}
			l.Color.XY = ev.Color.XY
		}
		if ev.ColorTemperature != nil {
			l.ColorTemperature = ev.ColorTemperature
		}
		c.lights[ev.ID] = l
	}
}

// Light returns the cached state of one light.
func (c *Cache) Light(id foxfire.ID) (foxfire.Light, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	l, ok := c.lights[id]
	return l, ok
}

// Lights returns a copy of all cached lights. It copies because handing out
// the map would make every caller's read a data race waiting to happen.
func (c *Cache) Lights() []foxfire.Light {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]foxfire.Light, 0, len(c.lights))
	for _, l := range c.lights {
		out = append(out, l)
	}
	return out
}

// LastEvent reports the creation time of the most recent applied batch. A
// stale value is the cheapest available signal that the stream has silently
// died -- worth watching in a daemon.
func (c *Cache) LastEvent() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastEvent
}

// Run seeds the cache and then folds events until ctx is done. It is the
// one-line path for callers who just want a live view.
func (c *Cache) Run(ctx context.Context, cl *foxfire.Client) error {
	if err := c.Seed(ctx, cl); err != nil {
		return err
	}
	batches, errs := cl.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b, ok := <-batches:
			if !ok {
				return nil
			}
			c.Apply(b)
		case err, ok := <-errs:
			if ok && err != nil {
				// Reported, not fatal: Subscribe reconnects on its own.
				_ = err
			}
		}
	}
}
