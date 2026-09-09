package grpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Exactly one call to the member stream's Send exists in this package: the
// closure StartStreaming hands to Register, which only the member's writer
// invokes. Any other raw write on the member stream would race the writer and
// corrupt HTTP/2 framing. Scoped to the member stream: entity.go and search.go
// write their own per-request server streams from a single goroutine.
func TestSingleWriter_OnlyOneRawSendOnTheMemberStream(t *testing.T) {
	fset := token.NewFileSet()
	total := 0
	for _, file := range []string{"streaming.go", "members.go", "dispatch.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Send" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "stream" {
				total++
				t.Logf("raw stream.Send at %s", fset.Position(call.Pos()))
			}
			return true
		})
	}
	if total != 1 {
		t.Fatalf("found %d raw stream.Send calls on the member stream; exactly one (the writer's closure) is allowed", total)
	}
}
