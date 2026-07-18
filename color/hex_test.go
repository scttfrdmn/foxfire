package color

import "testing"

func TestHex(t *testing.T) {
	cases := []struct {
		in         string
		r, g, b    float64
		shouldFail bool
	}{
		{in: "#ffffff", r: 1, g: 1, b: 1},
		{in: "000000", r: 0, g: 0, b: 0},
		{in: "#ff8800", r: 1, g: 0x88 / 255.0, b: 0},
		{in: "#f80", r: 1, g: 0x88 / 255.0, b: 0}, // shorthand equals ff8800
		{in: "  #FF0000  ", r: 1, g: 0, b: 0},     // trimmed, case-insensitive
		{in: "#12", shouldFail: true},
		{in: "nothex", shouldFail: true},
		{in: "#gggggg", shouldFail: true},
	}
	for _, tc := range cases {
		got, err := Hex(tc.in)
		if tc.shouldFail {
			if err == nil {
				t.Errorf("Hex(%q) should have failed, got %+v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Hex(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if !approx(got.R, tc.r, 1e-9) || !approx(got.G, tc.g, 1e-9) || !approx(got.B, tc.b, 1e-9) {
			t.Errorf("Hex(%q) = %+v, want {%.4f %.4f %.4f}", tc.in, got, tc.r, tc.g, tc.b)
		}
	}
}

// The shorthand and long forms must agree, and Update must carry the xy.
func TestUpdateCarriesXY(t *testing.T) {
	rgb, _ := Hex("#ff8800")
	u := Update(rgb)
	if u == nil || u.XY == nil {
		t.Fatal("Update returned no xy")
	}
	xy, _ := rgb.ToXY()
	if u.XY.X != xy.X || u.XY.Y != xy.Y {
		t.Errorf("Update xy = %+v, want %+v", *u.XY, xy)
	}
}
