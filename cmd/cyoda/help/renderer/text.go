package renderer

import (
	"fmt"
	"io"

	"github.com/charmbracelet/glamour"
)

// RenderText renders markdown to w using Charmbracelet glamour. style
// selects the ANSI theme:
//
//	""      → no ANSI (pipe/redirect)
//	"dark"  → dark terminal background (foreground-only highlights, no grey fill)
//	"light" → light terminal background (foreground-only highlights, no grey fill)
//
// Unknown style names fall back to the glamour notty (plain ASCII) preset.
// Returns the first error encountered constructing or running the renderer.
//
// CLI output uses fmt.Fprint to an injected writer — this is user-facing
// output, not operational logging. The log/slog rule does not apply here.
func RenderText(w io.Writer, body []byte, style string) error {
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(80)}
	switch style {
	case "dark":
		opts = append(opts, glamour.WithStyles(cyodaDarkStyle))
	case "light":
		opts = append(opts, glamour.WithStyles(cyodaLightStyle))
	default:
		opts = append(opts, glamour.WithStandardStyle("notty"))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return fmt.Errorf("render init: %w", err)
	}
	out, err := r.Render(string(body))
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	_, err = io.WriteString(w, out)
	return err
}
