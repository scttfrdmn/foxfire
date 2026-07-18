package color

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/foxfire"
)

// Hex parses a CSS-style hex color ("#ff8800", "ff8800", or the shorthand
// "#f80") into an RGB. It is here rather than in the wire package because it is
// a color-model convenience, not part of the protocol.
func Hex(s string) (RGB, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(h) {
	case 3: // shorthand: each nibble is doubled, so "f80" == "ff8800"
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6:
	default:
		return RGB{}, fmt.Errorf("color: %q is not a 3- or 6-digit hex color", s)
	}
	var r, g, b int
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b); err != nil {
		return RGB{}, fmt.Errorf("color: %q is not valid hex: %w", s, err)
	}
	return RGB{R: float64(r) / 255, G: float64(g) / 255, B: float64(b) / 255}, nil
}

// Update builds a light color update from an sRGB color, discarding the
// brightness component: the color update carries only chromaticity, and
// brightness travels through the separate dimming field. Set brightness with
// LightService.SetBrightness or a DimmingUpdate alongside this.
//
//	rgb, _ := color.Hex("#ff8800")
//	c.Lights.Update(ctx, id, foxfire.LightUpdate{Color: color.Update(rgb)})
func Update(c RGB) *foxfire.ColorUpdate {
	xy, _ := c.ToXY()
	return &foxfire.ColorUpdate{XY: &xy}
}

// UpdateInGamut is like Update but first clamps the color to what the target
// light can reproduce, using the gamut read from that light's Color field. Use
// this when you want to know the color will land where you asked rather than
// wherever the bridge silently moves it.
func UpdateInGamut(c RGB, g foxfire.Gamut) *foxfire.ColorUpdate {
	xy, _ := c.ToXY()
	xy = Clamp(xy, g)
	return &foxfire.ColorUpdate{XY: &xy}
}
