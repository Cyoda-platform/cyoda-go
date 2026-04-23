package renderer

import (
	"reflect"
	"testing"
)

func TestTokenize_Headings(t *testing.T) {
	tokens := Tokenize([]byte("# H1\n\n## H2\n\n### H3\n"))
	want := []Token{
		{Kind: KindHeading, Level: 1, Text: "H1"},
		{Kind: KindHeading, Level: 2, Text: "H2"},
		{Kind: KindHeading, Level: 3, Text: "H3"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_Paragraph(t *testing.T) {
	tokens := Tokenize([]byte("This is a paragraph.\n\nAnother paragraph.\n"))
	want := []Token{
		{Kind: KindParagraph, Text: "This is a paragraph."},
		{Kind: KindParagraph, Text: "Another paragraph."},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_Bullets(t *testing.T) {
	tokens := Tokenize([]byte("- one\n- two\n* three\n"))
	want := []Token{
		{Kind: KindBullet, Text: "one"},
		{Kind: KindBullet, Text: "two"},
		{Kind: KindBullet, Text: "three"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_FencedCode(t *testing.T) {
	tokens := Tokenize([]byte("```go\nfunc main() {}\n```\n"))
	want := []Token{
		{Kind: KindCodeBlock, Text: "func main() {}"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_HorizontalRule(t *testing.T) {
	tokens := Tokenize([]byte("before\n\n---\n\nafter\n"))
	want := []Token{
		{Kind: KindParagraph, Text: "before"},
		{Kind: KindRule},
		{Kind: KindParagraph, Text: "after"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestFindUnsupported_TableDetected(t *testing.T) {
	issues := FindUnsupported([]byte("| col |\n|-----|\n| val |\n"))
	if len(issues) == 0 {
		t.Fatal("pipe table must be flagged")
	}
}

func TestFindUnsupported_NestedListDetected(t *testing.T) {
	issues := FindUnsupported([]byte("- a\n  - nested\n"))
	if len(issues) == 0 {
		t.Fatal("nested list must be flagged")
	}
}

func TestFindUnsupported_HTMLBlockDetected(t *testing.T) {
	issues := FindUnsupported([]byte("<div>x</div>\n"))
	if len(issues) == 0 {
		t.Fatal("HTML block must be flagged")
	}
}

func TestFindUnsupported_CleanContentHasNoIssues(t *testing.T) {
	src := []byte("# Title\n\nParagraph text with **bold** and `code`.\n\n- bullet one\n- bullet two\n\n```\nfenced code\n```\n")
	issues := FindUnsupported(src)
	if len(issues) != 0 {
		t.Errorf("clean content flagged: %+v", issues)
	}
}

func TestTokenize_EmptyInput(t *testing.T) {
	if got := Tokenize(nil); len(got) != 0 {
		t.Errorf("Tokenize(nil) = %+v, want empty", got)
	}
	if got := Tokenize([]byte{}); len(got) != 0 {
		t.Errorf("Tokenize(empty) = %+v, want empty", got)
	}
}

func TestTokenize_UnterminatedFencedCode(t *testing.T) {
	// Unclosed fence: scanner exhausts without seeing closing ```.
	// Behaviour: accumulated body emitted as a KindCodeBlock.
	tokens := Tokenize([]byte("```\nline1\nline2\n"))
	if len(tokens) != 1 {
		t.Fatalf("len(tokens) = %d, want 1", len(tokens))
	}
	if tokens[0].Kind != KindCodeBlock {
		t.Errorf("tokens[0].Kind = %v, want KindCodeBlock", tokens[0].Kind)
	}
	if tokens[0].Text != "line1\nline2" {
		t.Errorf("tokens[0].Text = %q, want %q", tokens[0].Text, "line1\nline2")
	}
}

func TestFindUnsupported_PipeTableInsideFenceIsExempt(t *testing.T) {
	// Content that LOOKS like a pipe table but is inside a fenced code
	// block must not be flagged — example usage in help docs is legitimate.
	src := []byte("```\n| col |\n|-----|\n| val |\n```\n")
	if issues := FindUnsupported(src); len(issues) != 0 {
		t.Errorf("fenced pipe table flagged: %+v", issues)
	}
}
