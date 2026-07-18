// Package color converts between the CIE xy chromaticity the bridge speaks and
// the RGB everyone actually thinks in, and clamps colors to what a given light
// can physically reproduce.
//
// It is a separate package on purpose. The wire types carry xy because that is
// what the bridge sends; nobody composing a scene thinks in xy. But baking RGB
// conversion into the wire types would drag a color model and a pile of matrix
// constants into every program that only ever reads state. Callers who want
// color math import this; callers who do not, do not.
//
// The conversion uses the standard sRGB <-> CIE XYZ matrices at the D65 white
// point, rather than the slightly-off "Wide-RGB" matrix that circulates in
// older Hue examples. That choice makes the endpoints exact and checkable --
// sRGB red lands on (0.64, 0.33), white on D65 (0.3127, 0.3290) -- at the cost
// of not exactly matching whatever the lamp firmware assumes internally. Since
// a lamp clamps to its own gamut anyway, and this is an approximation either
// way, "correct and testable" wins over "bug-compatible with the app".
//
// Out-of-gamut colors are clamped rather than rejected, because that is what
// the bridge itself does. A caller that sends an unreproducible xy gets the
// nearest reproducible one, silently; this package makes that clamping explicit
// and inspectable instead.
package color

import (
	"math"

	"github.com/scttfrdmn/foxfire"
)

// RGB is a non-linear sRGB color, each channel in [0, 1]. This is the space of
// a CSS hex string or an image pixel, gamma included.
type RGB struct {
	R, G, B float64
}

// ToXY converts an sRGB color to a CIE xy chromaticity plus a relative
// brightness Y in [0, 1]. The brightness is returned because xy alone throws
// it away: pure xy carries hue and saturation but not how bright the color is,
// and the bridge takes brightness through a separate dimming field. A caller
// setting both color and brightness wants this Y; one setting only color can
// ignore it.
func (c RGB) ToXY() (foxfire.XY, float64) {
	r := gammaExpand(c.R)
	g := gammaExpand(c.G)
	b := gammaExpand(c.B)

	// Standard sRGB -> CIE XYZ at D65 (IEC 61966-2-1).
	x := r*0.4124564 + g*0.3575761 + b*0.1804375
	y := r*0.2126729 + g*0.7151522 + b*0.0721750
	z := r*0.0193339 + g*0.1191920 + b*0.9503041

	sum := x + y + z
	if sum == 0 {
		// Black has no defined chromaticity. Return the D65 white point so the
		// result is at least in-gamut, with zero brightness to say "it's off".
		return foxfire.XY{X: 0.3127, Y: 0.3290}, 0
	}
	return foxfire.XY{X: x / sum, Y: y / sum}, clamp01(y)
}

// FromXY converts a CIE xy chromaticity and a relative brightness Y in [0, 1]
// back to sRGB. Pass Y = 1 if you only care about hue. The result is clamped
// into [0, 1] per channel and, if any channel would exceed 1, the whole color
// is scaled down so the hue is preserved rather than the color desaturating
// toward white.
func FromXY(xy foxfire.XY, brightness float64) RGB {
	y := xy.Y
	if y == 0 {
		return RGB{}
	}
	bri := clamp01(brightness)

	// Reconstruct XYZ from xy and Y.
	capY := bri
	capX := (capY / y) * xy.X
	capZ := (capY / y) * (1 - xy.X - y)

	// Inverse: CIE XYZ -> linear sRGB at D65.
	r := capX*3.2404542 - capY*1.5371385 - capZ*0.4985314
	g := -capX*0.9692660 + capY*1.8760108 + capZ*0.0415560
	b := capX*0.0556434 - capY*0.2040259 + capZ*1.0572252

	// A negative channel means the color is outside the sRGB gamut; lift the
	// whole color so the smallest channel reaches zero, preserving hue.
	if m := math.Min(r, math.Min(g, b)); m < 0 {
		r -= m
		g -= m
		b -= m
	}
	// If any channel now exceeds 1, scale all three down together.
	if m := math.Max(r, math.Max(g, b)); m > 1 {
		r /= m
		g /= m
		b /= m
	}

	return RGB{
		R: gammaCompress(r),
		G: gammaCompress(g),
		B: gammaCompress(b),
	}
}

// Clamp returns the point inside gamut nearest to xy. If xy is already inside,
// it is returned unchanged. The bridge does this clamping itself and silently,
// which is a frequent "why is the color wrong" surprise; call this first to see
// where a requested color will actually land.
func Clamp(xy foxfire.XY, g foxfire.Gamut) foxfire.XY {
	p := point{xy.X, xy.Y}
	r := point{g.Red.X, g.Red.Y}
	gr := point{g.Green.X, g.Green.Y}
	bl := point{g.Blue.X, g.Blue.Y}

	if inTriangle(p, r, gr, bl) {
		return xy
	}

	// Closest point on each edge of the gamut triangle; pick the nearest.
	ab := closestOnSegment(p, r, gr)
	ac := closestOnSegment(p, r, bl)
	bc := closestOnSegment(p, gr, bl)

	dab := dist2(p, ab)
	dac := dist2(p, ac)
	dbc := dist2(p, bc)

	best := ab
	bd := dab
	if dac < bd {
		best, bd = ac, dac
	}
	if dbc < bd {
		best = bc
	}
	return foxfire.XY{X: best.x, Y: best.y}
}

// InGamut reports whether xy is reproducible within the given gamut.
func InGamut(xy foxfire.XY, g foxfire.Gamut) bool {
	return inTriangle(
		point{xy.X, xy.Y},
		point{g.Red.X, g.Red.Y},
		point{g.Green.X, g.Green.Y},
		point{g.Blue.X, g.Blue.Y},
	)
}

// --- sRGB gamma ---

func gammaExpand(c float64) float64 {
	c = clamp01(c)
	if c > 0.04045 {
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return c / 12.92
}

func gammaCompress(c float64) float64 {
	c = clamp01(c)
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// --- planar geometry on the xy plane ---

type point struct{ x, y float64 }

func dist2(a, b point) float64 {
	dx := a.x - b.x
	dy := a.y - b.y
	return dx*dx + dy*dy
}

// inTriangle uses the sign-of-cross-product test with a small tolerance.
// Points on an edge count as inside, so an xy sitting exactly on a primary is
// not needlessly moved -- and, importantly, a point that Clamp has just placed
// on the boundary reads as in-gamut despite floating-point rounding, rather
// than appearing to sit an epsilon outside the edge it was snapped to.
func inTriangle(p, a, b, c point) bool {
	const eps = 1e-9
	d1 := cross(p, a, b)
	d2 := cross(p, b, c)
	d3 := cross(p, c, a)
	hasNeg := d1 < -eps || d2 < -eps || d3 < -eps
	hasPos := d1 > eps || d2 > eps || d3 > eps
	return !(hasNeg && hasPos)
}

func cross(p, a, b point) float64 {
	return (p.x-b.x)*(a.y-b.y) - (a.x-b.x)*(p.y-b.y)
}

// closestOnSegment returns the point on segment ab nearest to p.
func closestOnSegment(p, a, b point) point {
	abx := b.x - a.x
	aby := b.y - a.y
	denom := abx*abx + aby*aby
	if denom == 0 {
		return a
	}
	t := ((p.x-a.x)*abx + (p.y-a.y)*aby) / denom
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return point{a.x + t*abx, a.y + t*aby}
}
