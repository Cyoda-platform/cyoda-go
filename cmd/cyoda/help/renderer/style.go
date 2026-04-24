package renderer

import (
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// cyodaLightStyle is glamour's LightStyleConfig with background fills removed
// from inline code spans and fenced code blocks, and brand aqua foreground
// applied. #4FB8B0 is a softer, mint-leaning aqua tuned for contrast on white
// terminals.
var cyodaLightStyle = applyCyodaTheme(styles.LightStyleConfig, "#4FB8B0") // brand aqua (light)

// cyodaDarkStyle is glamour's DarkStyleConfig with the same background-fill
// suppression and brand aqua (#5FD7D7) foreground applied. This matches the
// 256-color index 80 used in the cyoda banner.
var cyodaDarkStyle = applyCyodaTheme(styles.DarkStyleConfig, "#5FD7D7") // brand aqua (banner)

// applyCyodaTheme copies the preset, clears the grey code backgrounds,
// and sets teal foreground colors for inline code and plain fenced
// blocks. Chroma (syntax highlighting) is untouched — language-tagged
// fences retain their full syntax color palette.
//
// The Chroma pointer is deep-copied so the global preset is never mutated.
// Chroma.Error.BackgroundColor is intentionally left intact — it marks syntax
// errors and is not cosmetic fill.
func applyCyodaTheme(base ansi.StyleConfig, teal string) ansi.StyleConfig {
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
		// Bold on the Chroma Text token makes plain (untagged) fenced blocks
		// bold. Language-tagged fences inherit their own token styles from the
		// Chroma theme — only the Text (plain-text) token is affected here.
		chromaCopy.Text.Bold = boolPtr(true)
		s.CodeBlock.Chroma = &chromaCopy
	}

	// Set teal foreground on inline code and the plain fenced-block wrapper.
	// strPtr allocates separate *string values per field.
	s.Code.StylePrimitive.Color = strPtr(teal)
	s.CodeBlock.StyleBlock.StylePrimitive.Color = strPtr(teal)

	// Bold makes teal code pop against body text without needing backgrounds.
	s.Code.Bold = boolPtr(true)
	s.CodeBlock.StyleBlock.Bold = boolPtr(true)

	return s
}

// strPtr returns a pointer to a copy of s. Used to set *string fields in
// ansi.StylePrimitive without aliasing issues.
func strPtr(s string) *string { return &s }

// boolPtr returns a pointer to b. Used to set *bool fields in
// ansi.StylePrimitive without aliasing issues.
func boolPtr(b bool) *bool { return &b }
