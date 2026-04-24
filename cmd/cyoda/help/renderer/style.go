package renderer

import (
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// cyodaLightStyle is glamour's LightStyleConfig with background fills removed
// from inline code spans and fenced code blocks, brand logo-teal foreground
// applied to code, and deep-blue foreground applied to headings (with heading
// backgrounds cleared). #118080 is the exact logo teal from the Cyoda brand.
var cyodaLightStyle = applyCyodaTheme(styles.LightStyleConfig, "#118080", "#081780")

// cyodaDarkStyle is glamour's DarkStyleConfig with the same background-fill
// suppression and brand aqua (#5FD7D7) foreground applied to code. This matches
// the 256-color index 80 used in the cyoda banner. Headings are left at the
// dark preset defaults (no headingColor).
var cyodaDarkStyle = applyCyodaTheme(styles.DarkStyleConfig, "#5FD7D7", "")

// applyCyodaTheme copies the preset, applies cyoda brand colors:
//   - teal on inline code and plain fenced blocks (+bold, backgrounds cleared)
//   - headingColor on all H1–H6 (background cleared)
//
// If headingColor is empty, headings are left untouched.
//
// The Chroma pointer is deep-copied so the global preset is never mutated.
// Chroma.Error.BackgroundColor is intentionally left intact — it marks syntax
// errors and is not cosmetic fill.
func applyCyodaTheme(base ansi.StyleConfig, teal, headingColor string) ansi.StyleConfig {
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

	// Headings: apply deep-blue foreground and clear background fills.
	// Only applied when headingColor is non-empty (light theme).
	if headingColor != "" {
		applyHeadingColor(&s.Heading, headingColor)
		applyHeadingColor(&s.H1, headingColor)
		applyHeadingColor(&s.H2, headingColor)
		applyHeadingColor(&s.H3, headingColor)
		applyHeadingColor(&s.H4, headingColor)
		applyHeadingColor(&s.H5, headingColor)
		applyHeadingColor(&s.H6, headingColor)
	}

	return s
}

// applyHeadingColor sets the heading foreground to color and clears any
// background fill. Existing Bold is preserved — glamour's presets set headings
// bold by default and we want to keep that.
func applyHeadingColor(b *ansi.StyleBlock, color string) {
	b.Color = strPtr(color)
	b.BackgroundColor = nil
}

// strPtr returns a pointer to a copy of s. Used to set *string fields in
// ansi.StylePrimitive without aliasing issues.
func strPtr(s string) *string { return &s }

// boolPtr returns a pointer to b. Used to set *bool fields in
// ansi.StylePrimitive without aliasing issues.
func boolPtr(b bool) *bool { return &b }
