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
//	"dark"  → dark terminal background
//	"light" → light terminal background
//
// Unknown style names fall back to the glamour default for the style.
// Returns the first error encountered constructing or running the renderer.
//
// CLI output uses fmt.Fprint to an injected writer — this is user-facing
// output, not operational logging. The log/slog rule does not apply here.
func RenderText(w io.Writer, body []byte, style string) error {
	if style == "" {
		style = "notty"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(80),
	)
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
