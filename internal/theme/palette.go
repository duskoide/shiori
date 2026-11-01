package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Palette is a terminal palette. Chrome colours are derived from it by Resolve.
type Palette struct {
	Background, Foreground, Selection Color
	// Cursor defaults to Foreground when its alpha is zero (unset).
	Cursor Color
	ANSI   [16]Color
	// Status holds the working, blocked and done colours. When nil they come
	// from ANSI yellow (3), red (1) and blue (4).
	Status *[3]Color
	// Overrides replaces derived tokens by name (e.g. "border", "ansi3");
	// applied by Resolve. Values are parsed with ParseHex.
	Overrides map[string]Color
}

var darkStatus = [3]Color{rgb(0xe5c07b), rgb(0xf0705f), rgb(0x76a8ff)}

func status(s [3]Color) *[3]Color { return &s }

func ansi16(v [16]uint32) (out [16]Color) {
	for i, c := range v {
		out[i] = rgb(c)
	}
	return
}

var presets = map[string]Palette{
	"graphite": {
		Background: rgb(0x17191d), Foreground: rgb(0xd2d4d9), Selection: rgb(0x394454),
		Status: status(darkStatus),
		ANSI: ansi16([16]uint32{0x45484f, 0xdc8c95, 0xa6bd91, 0xd8be87, 0x96b2d7, 0xb5a2ce, 0x8cbdc3, 0xd2d4d9,
			0x818793, 0xf0a5ad, 0xbad5a2, 0xead2a1, 0xb0cbef, 0xcbb7e6, 0xa4d5db, 0xf0f0f2}),
	},
	"ink": {
		Background: rgb(0x111720), Foreground: rgb(0xcdd6e4), Selection: rgb(0x344861),
		Status: status(darkStatus),
		ANSI: ansi16([16]uint32{0x414b5a, 0xe58c9a, 0x89c5aa, 0xd9be82, 0x91b6ee, 0xb9a0e2, 0x85c4d0, 0xcdd6e4,
			0x7f91a7, 0xf5a5b2, 0xa5dec3, 0xedd39c, 0xaccbfa, 0xd0b7f4, 0xa0dce5, 0xf0f5fc}),
	},
	"moss": {
		Background: rgb(0x151c19), Foreground: rgb(0xcdd7ce), Selection: rgb(0x354a40),
		Status: status(darkStatus),
		ANSI: ansi16([16]uint32{0x435248, 0xdf978d, 0xa0c4a2, 0xd4bc8b, 0x99b9cd, 0xbba4c8, 0x8bbdb5, 0xcdd7ce,
			0x82968a, 0xf1afa3, 0xb8dab9, 0xe9d2a5, 0xb1d0e2, 0xd1bddc, 0xa4d6ca, 0xeff5ee}),
	},
	"paper": {
		Background: rgb(0xffffff), Foreground: rgb(0x25272c), Selection: rgb(0xcfdcf5), Cursor: rgb(0x25272c),
		Status: status([3]Color{rgb(0x9a6700), rgb(0xc93c2c), rgb(0x2f6fe4)}),
		ANSI: ansi16([16]uint32{0x25272c, 0xc62a2a, 0x1a7f37, 0x8f5d00, 0x1f62d0, 0x7a45c8, 0x0f6f74, 0x5f6068,
			0x7b7e87, 0xd1242f, 0x1f883d, 0x9a6700, 0x0969da, 0x8250df, 0x1b7c83, 0x111214}),
	},
	"atom-one-dark": {
		Background: rgb(0x21252b), Foreground: rgb(0xabb2bf), Selection: rgb(0x323844), Cursor: rgb(0xabb2bf),
		ANSI: ansi16([16]uint32{0x454c59, 0xe06c75, 0x98c379, 0xe5c07b, 0x61afef, 0xc678dd, 0x56b6c2, 0xabb2bf,
			0x767676, 0xe06c75, 0x98c379, 0xe5c07b, 0x61afef, 0xc678dd, 0x56b6c2, 0xabb2bf}),
	},
}

// PresetNames lists the built-in palette names in display order.
var PresetNames = []string{"graphite", "ink", "moss", "paper", "atom-one-dark"}

// Preset returns a built-in palette: graphite, ink, moss, paper or atom-one-dark.
// The result is a copy and may be modified.
func Preset(name string) (Palette, bool) {
	p, ok := presets[name]
	if ok && p.Status != nil {
		p.Status = status(*p.Status)
	}
	return p, ok
}

type themeFile struct {
	Background string            `json:"background"`
	Foreground string            `json:"foreground"`
	Selection  string            `json:"selection"`
	Cursor     *string           `json:"cursor"`
	ANSI       []string          `json:"ansi"`
	Colors     map[string]string `json:"colors"`
}

// LoadPalette parses a theme file: {background, foreground, selection,
// cursor?, ansi:[16], colors?:{token:hex}}. Unknown fields are rejected.
// Base colours must be #rrggbb; override values may carry an alpha.
func LoadPalette(b []byte) (Palette, error) {
	var f themeFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Palette{}, fmt.Errorf("theme: %w", err)
	}
	var p Palette
	opaque := func(name, v string) (Color, error) {
		c, err := ParseHex(v)
		if err != nil || len(strings.TrimPrefix(strings.TrimSpace(v), "#")) != 6 {
			return Color{}, fmt.Errorf("theme: %s %q is not #rrggbb", name, v)
		}
		return c, nil
	}
	var err error
	if p.Background, err = opaque("background", f.Background); err != nil {
		return Palette{}, err
	}
	if p.Foreground, err = opaque("foreground", f.Foreground); err != nil {
		return Palette{}, err
	}
	if p.Selection, err = opaque("selection", f.Selection); err != nil {
		return Palette{}, err
	}
	if f.Cursor != nil {
		if p.Cursor, err = opaque("cursor", *f.Cursor); err != nil {
			return Palette{}, err
		}
	}
	if len(f.ANSI) != 16 {
		return Palette{}, fmt.Errorf("theme: ansi needs 16 colours, got %d", len(f.ANSI))
	}
	for i, s := range f.ANSI {
		if p.ANSI[i], err = opaque(fmt.Sprintf("ansi[%d]", i), s); err != nil {
			return Palette{}, err
		}
	}
	if len(f.Colors) > 0 {
		p.Overrides = make(map[string]Color, len(f.Colors))
		for k, v := range f.Colors {
			c, err := ParseHex(v)
			if err != nil {
				return Palette{}, fmt.Errorf("theme: colors.%s: %q is not #rrggbb or #rrggbbaa", k, v)
			}
			p.Overrides[k] = c
		}
	}
	return p, nil
}
