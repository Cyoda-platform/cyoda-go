package renderer

import (
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// cyodaLightStyle is glamour's LightStyleConfig with background fills removed
// from inline code spans and fenced code blocks. Glamour's built-in light
// preset renders inline code with a pale grey background (ANSI color 254)
// that looks washed out on white terminals. This variant keeps the foreground
// color highlighting while suppressing the fill.
var cyodaLightStyle = clearCodeBackgrounds(styles.LightStyleConfig)

// cyodaDarkStyle is glamour's DarkStyleConfig with the same background-fill
// suppression applied for visual consistency.
var cyodaDarkStyle = clearCodeBackgrounds(styles.DarkStyleConfig)

// clearCodeBackgrounds returns a copy of base with BackgroundColor cleared on:
//   - Code (inline code spans): base.Code.StylePrimitive.BackgroundColor
//   - CodeBlock wrapper: base.CodeBlock.StyleBlock.StylePrimitive.BackgroundColor
//   - Chroma block background: base.CodeBlock.Chroma.Background.BackgroundColor
//
// The Chroma pointer is deep-copied so the global preset is never mutated.
// Chroma.Error.BackgroundColor is intentionally left intact — it marks syntax
// errors and is not cosmetic fill.
func clearCodeBackgrounds(base ansi.StyleConfig) ansi.StyleConfig {
	s := base

	// Clear inline-code background.
	s.Code.StylePrimitive.BackgroundColor = nil

	// Clear fenced code-block wrapper background.
	s.CodeBlock.StyleBlock.StylePrimitive.BackgroundColor = nil

	// Deep-copy the Chroma pointer before modifying to avoid mutating the
	// package-level preset.
	if base.CodeBlock.Chroma != nil {
		chromaCopy := *base.CodeBlock.Chroma
		chromaCopy.Background.BackgroundColor = nil
		s.CodeBlock.Chroma = &chromaCopy
	}

	return s
}
