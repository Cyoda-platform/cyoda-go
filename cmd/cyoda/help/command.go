// Package help — CLI dispatch for the `cyoda help` subcommand.
//
// CLI output uses fmt.Fprint to injected writers — this is user-facing
// output, not operational logging. The log/slog rule applies to
// slog-ingested diagnostic events, not stdout.
package help

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
)

// RunHelp dispatches `cyoda help [args...]`. Returns the intended exit
// code: 0 on success, 2 on unknown topic / bad args.
//
//	tree    — the resolved topic tree
//	args    — positional and --format args after "help"
//	out     — stdout of the CLI
//	version — binary version string for HelpPayload.Version
//	isTTY   — whether out is a TTY (governs text vs markdown default)
func RunHelp(tree *Tree, args []string, out io.Writer, version string, isTTY bool) int {
	format := "auto"
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "--format=") {
			format = strings.TrimPrefix(a, "--format=")
			continue
		}
		if a == "--format" {
			fmt.Fprintln(out, "cyoda help: --format requires = value (e.g. --format=json)")
			return 2
		}
		positional = append(positional, a)
	}

	// No positional: render tree summary. In json mode emit the full payload.
	if len(positional) == 0 {
		if format == "json" {
			return writeFullTreeJSON(tree, out, version)
		}
		return writeTreeSummary(tree, out, isTTY)
	}

	// Topic lookup.
	topic := tree.Find(positional)
	if topic == nil {
		writeUnknownTopicError(tree, positional, out)
		return 2
	}

	switch resolveFormat(format, isTTY) {
	case "json":
		return writeTopicJSON(topic, out)
	case "markdown":
		return writeTopicMarkdown(topic, out)
	default:
		return writeTopicText(topic, out, isTTY)
	}
}

func resolveFormat(f string, isTTY bool) string {
	switch f {
	case "json", "markdown", "text":
		return f
	case "auto", "":
		if isTTY {
			return "text"
		}
		return "markdown"
	default:
		return "text"
	}
}

func writeTopicText(t *Topic, out io.Writer, isTTY bool) int {
	toks := renderer.Tokenize(t.Body)
	renderer.RenderText(out, toks, isTTY)
	if len(t.SeeAlso) > 0 {
		fmt.Fprintln(out, "\nSEE ALSO")
		for _, s := range t.SeeAlso {
			fmt.Fprintf(out, "  • %s\n", s)
		}
	}
	return 0
}

func writeTopicMarkdown(t *Topic, out io.Writer) int {
	renderer.RenderMarkdown(out, t.Body, t.SeeAlso)
	return 0
}

func writeTopicJSON(t *Topic, out io.Writer) int {
	d := t.Descriptor()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d)
	return 0
}

func writeFullTreeJSON(tree *Tree, out io.Writer, version string) int {
	payload := renderer.HelpPayload{
		Schema:  1,
		Version: version,
		Topics:  tree.WalkDescriptors(),
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
	return 0
}

func writeTreeSummary(tree *Tree, out io.Writer, isTTY bool) int {
	fmt.Fprintln(out, "cyoda help — topic reference")
	fmt.Fprintln(out)
	buckets := map[string][]*Topic{}
	for _, t := range tree.Root.Children {
		buckets[t.Stability] = append(buckets[t.Stability], t)
	}
	for _, stab := range []string{"stable", "evolving", "experimental"} {
		list := buckets[stab]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Path[0] < list[j].Path[0]
		})
		title := bucketTitle(stab)
		fmt.Fprintln(out, title)
		for _, t := range list {
			fmt.Fprintf(out, "  %-16s %s\n", t.Path[0], renderer.ExtractSynopsis(t.Body))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Run 'cyoda help <topic>' for details.")
	return 0
}

// bucketTitle returns the human-readable bucket header for a stability level.
// (strings.Title is deprecated for Unicode reasons — we enumerate explicitly.)
func bucketTitle(stab string) string {
	switch stab {
	case "stable":
		return "Stable"
	case "evolving":
		return "Evolving"
	case "experimental":
		return "Experimental — content pending"
	default:
		return stab
	}
}

func writeUnknownTopicError(tree *Tree, args []string, out io.Writer) {
	// Find the nearest existing parent and list its children.
	parent := tree.Root
	matched := 0
	for i, seg := range args {
		found := false
		for _, c := range parent.Children {
			if len(c.Path) > 0 && c.Path[len(c.Path)-1] == seg {
				parent = c
				matched = i + 1
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	if matched >= len(args) {
		// Defensive: should not happen because Find would have returned non-nil.
		fmt.Fprintf(out, "cyoda help: topic lookup failed for %q\n", strings.Join(args, " "))
		return
	}
	missing := args[matched]
	if matched == 0 {
		fmt.Fprintf(out, "cyoda help: no such topic: %q. Run 'cyoda help' to list available topics.\n", missing)
		return
	}
	parentPath := strings.Join(args[:matched], " ")
	var kids []string
	for _, c := range parent.Children {
		kids = append(kids, c.Path[len(c.Path)-1])
	}
	sort.Strings(kids)
	fmt.Fprintf(out, "cyoda help: topic %q has no subtopic %q. Available: %s. Run 'cyoda help %s' for an overview.\n",
		parentPath, missing, strings.Join(kids, ", "), parentPath)
}
