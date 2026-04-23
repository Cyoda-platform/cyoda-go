package renderer

import (
	"fmt"
	"io"

	"github.com/charmbracelet/glamour"
)

// RenderText renders markdown to w using Charmbracelet glamour.
// On TTY, emits ANSI-styled output with syntax-highlighted code blocks.
// Off TTY, emits plain text with markdown markers removed.
// Returns the first error encountered constructing or running the renderer.
//
// CLI output uses fmt.Fprint to an injected writer — this is user-facing
// output, not operational logging. The log/slog rule does not apply here.
func RenderText(w io.Writer, body []byte, isTTY bool) error {
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(80),
	}
	if isTTY {
		// Use the dark theme as the default TTY style. A future enhancement
		// could detect the terminal background colour via termenv and select
		// "light" dynamically, but that requires passing a terminal reference
		// into this function — deferred until there is a real user request.
		opts = append(opts, glamour.WithStandardStyle("dark"))
	} else {
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
