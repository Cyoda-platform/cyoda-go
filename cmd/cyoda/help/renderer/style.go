package renderer

import (
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// cyodaLightStyle is glamour's LightStyleConfig with background fills removed
// from inline code spans and fenced code blocks, and teal foreground colors
// applied. Dark teal (#008080) is used for readability on white terminals.
var cyodaLightStyle = applyCyodaTheme(styles.LightStyleConfig, "#008080") // CSS teal

// cyodaDarkStyle is glamour's DarkStyleConfig with the same background-fill
// suppression and bright teal (#5FDDD7) foreground applied for readability on
// dark terminals.
var cyodaDarkStyle = applyCyodaTheme(styles.DarkStyleConfig, "#5FDDD7") // bright teal

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
		s.CodeBlock.Chroma = &chromaCopy
	}

	// Set teal foreground on inline code and the plain fenced-block wrapper.
	// strPtr allocates separate *string values per field.
	s.Code.StylePrimitive.Color = strPtr(teal)
	s.CodeBlock.StyleBlock.StylePrimitive.Color = strPtr(teal)

	return s
}

// strPtr returns a pointer to a copy of s. Used to set *string fields in
// ansi.StylePrimitive without aliasing issues.
func strPtr(s string) *string { return &s }
