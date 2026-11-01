// Package assets holds Shiori's logo: tools/logo/make_logo.py (make logo)
// draws the files beside this one from Noto Sans CJK JP.
package assets

import _ "embed"

// Icon is the app icon, a 1024×1024 PNG of 栞 on an ink tile; packages
// take it from icon.png too (mygo.json).
//
//go:embed icon.png
var Icon []byte

// Mark is the bare 栞, an SVG in currentColor, 24×24.
//
//go:embed mark.svg
var Mark string
