package state

import (
	"testing"

	"github.com/scttfrdmn/foxfire"
)

// The central invariant of the cache: a delta that omits a field must leave
// that field alone. An on/off event carries no dimming, and folding it must
// not reset brightness to zero.
func TestApplyPreservesUnmentionedFields(t *testing.T) {
	c := New()
	c.lights["abc"] = foxfire.Light{
		ID:      "abc",
		On:      foxfire.On{On: true},
		Dimming: &foxfire.Dimming{Brightness: 75},
	}

	c.Apply(foxfire.EventBatch{
		Type: "update",
		Events: []foxfire.Event{{
			ID:   "abc",
			Type: foxfire.TypeLight,
			On:   &foxfire.On{On: false},
			// Dimming deliberately nil.
		}},
	})

	l, ok := c.Light("abc")
	if !ok {
		t.Fatal("light vanished from cache")
	}
	if l.On.On {
		t.Error("on state was not applied")
	}
	if l.Dimming == nil || l.Dimming.Brightness != 75 {
		t.Errorf("brightness was clobbered by an on/off event: %+v", l.Dimming)
	}
}

func TestApplyInsertsUnknownLights(t *testing.T) {
	c := New()
	c.Apply(foxfire.EventBatch{
		Type: "update",
		Events: []foxfire.Event{{
			ID:      "new",
			Type:    foxfire.TypeLight,
			Dimming: &foxfire.Dimming{Brightness: 10},
		}},
	})

	if l, ok := c.Light("new"); !ok || l.Dimming.Brightness != 10 {
		t.Errorf("unknown light was dropped instead of inserted: %+v, %v", l, ok)
	}
}

func TestApplyIgnoresNonLightResources(t *testing.T) {
	c := New()
	c.Apply(foxfire.EventBatch{
		Type: "update",
		Events: []foxfire.Event{{
			ID:     "motion-1",
			Type:   foxfire.TypeMotion,
			Motion: &foxfire.MotionReport{Motion: true},
		}},
	})
	if len(c.Lights()) != 0 {
		t.Error("a motion event created a light")
	}
}

func TestLightsReturnsACopy(t *testing.T) {
	c := New()
	c.lights["abc"] = foxfire.Light{ID: "abc", On: foxfire.On{On: true}}

	got := c.Lights()
	got[0].On.On = false

	if l, _ := c.Light("abc"); !l.On.On {
		t.Error("mutating the returned slice mutated the cache")
	}
}
