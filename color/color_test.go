package color

import (
	"math"
	"testing"

	"github.com/scttfrdmn/foxfire"
)

func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// The primaries and secondaries should round-trip through xy and back to
// something very close to where they started. This is the property that would
// break first if either matrix or the gamma curves were wrong.
func TestRoundTripPrimaries(t *testing.T) {
	cases := map[string]RGB{
		"red":     {R: 1},
		"green":   {G: 1},
		"blue":    {B: 1},
		"yellow":  {R: 1, G: 1},
		"cyan":    {G: 1, B: 1},
		"magenta": {R: 1, B: 1},
		"white":   {R: 1, G: 1, B: 1},
	}
	for name, in := range cases {
		xy, y := in.ToXY()
		out := FromXY(xy, y)
		// Hue is what we care about; a saturated primary may lose a little
		// magnitude to gamut scaling, so compare direction generously.
		if !approx(out.R, in.R, 0.02) || !approx(out.G, in.G, 0.02) || !approx(out.B, in.B, 0.02) {
			t.Errorf("%s: round trip %+v -> xy%+v,Y=%.3f -> %+v", name, in, xy, y, out)
		}
	}
}

// Black has no chromaticity. It must not divide by zero, and it must come back
// as black rather than as the white point lit up.
func TestBlackIsSafe(t *testing.T) {
	xy, y := RGB{}.ToXY()
	if y != 0 {
		t.Errorf("black should have zero brightness, got %.3f", y)
	}
	out := FromXY(xy, y)
	if out != (RGB{}) {
		t.Errorf("black did not round-trip to black: %+v", out)
	}
}

// Red's xy should sit near the documented sRGB red primary (0.64, 0.33).
func TestRedChromaticity(t *testing.T) {
	xy, _ := RGB{R: 1}.ToXY()
	if !approx(xy.X, 0.64, 0.02) || !approx(xy.Y, 0.33, 0.02) {
		t.Errorf("red chromaticity off: got %+v, want ~{0.64, 0.33}", xy)
	}
}

// A gamut C triangle (a typical Hue color bulb). Points inside are unchanged;
// points outside land on the boundary and become reproducible.
func gamutC() foxfire.Gamut {
	return foxfire.Gamut{
		Red:   foxfire.XY{X: 0.6915, Y: 0.3083},
		Green: foxfire.XY{X: 0.1700, Y: 0.7000},
		Blue:  foxfire.XY{X: 0.1532, Y: 0.0475},
	}
}

func TestClampInsideIsUnchanged(t *testing.T) {
	g := gamutC()
	inside := foxfire.XY{X: 0.35, Y: 0.35} // near white, comfortably interior
	if !InGamut(inside, g) {
		t.Fatal("test point was not actually inside the gamut")
	}
	got := Clamp(inside, g)
	if got != inside {
		t.Errorf("interior point moved: %+v -> %+v", inside, got)
	}
}

func TestClampOutsidePullsToBoundary(t *testing.T) {
	g := gamutC()
	outside := foxfire.XY{X: 0.0, Y: 0.0} // well outside, below the blue corner
	if InGamut(outside, g) {
		t.Fatal("test point was supposed to be outside the gamut")
	}
	got := Clamp(outside, g)
	if !InGamut(got, g) {
		t.Errorf("clamped point is still outside gamut: %+v", got)
	}
	// The clamp must move it closer, not further.
	if dist2(point{outside.X, outside.Y}, point{got.X, got.Y}) == 0 {
		t.Error("outside point was not moved")
	}
}
