// Package theme holds herdr's colour palettes and derives the chrome colour
// tokens (surfaces, borders, status colours) from them.
package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Color is a non-premultiplied 8-bit RGBA colour.
type Color struct{ R, G, B, A uint8 }

// rgb builds an opaque colour from 0xRRGGBB.
func rgb(v uint32) Color {
	return Color{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

// ParseHex parses "#rrggbb" or "#rrggbbaa" (the leading '#' is optional).
func ParseHex(s string) (Color, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 && len(h) != 8 {
		return Color{}, fmt.Errorf("theme: %q is not #rrggbb or #rrggbbaa", s)
	}
	for _, r := range h {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return Color{}, fmt.Errorf("theme: %q is not #rrggbb or #rrggbbaa", s)
		}
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("theme: %q: %w", s, err)
	}
	if len(h) == 6 {
		return rgb(uint32(v)), nil
	}
	return Color{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// Hex formats the colour as "#rrggbb", or "#rrggbbaa" when not opaque.
func (c Color) Hex() string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// Mix interpolates from a (t=0) to b (t=1) per channel, rounding to nearest.
func Mix(a, b Color, t float64) Color {
	f := func(x, y uint8) uint8 {
		v := float32(x)*(1-float32(t)) + float32(y)*float32(t)
		return uint8(math.Max(0, math.Min(255, math.Round(float64(v)))))
	}
	return Color{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), f(a.A, b.A)}
}

// luminance is the relative luma of c on a 0..1 scale (Rec. 709 weights on
// gamma-encoded channels, as herdr uses to tell light themes from dark).
func luminance(c Color) float64 {
	return 0.2126*float64(c.R)/255 + 0.7152*float64(c.G)/255 + 0.0722*float64(c.B)/255
}
