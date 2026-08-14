package compatapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Sentinels is written by hand, so it can drift from the declarations it
// claims to enumerate — and a sentinel missing from it reaches a translating
// caller's default arm as an opaque server error, which is exactly the failure
// the list exists to prevent. Read the declarations instead of trusting them.
func TestSentinelsEnumeratesEveryDeclaredSentinel(t *testing.T) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v", err)
	}

	declared := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if len(name.Name) < 4 || name.Name[:3] != "Err" || i >= len(spec.Values) {
				continue
			}
			call, ok := spec.Values[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "New" {
				continue
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "errors" {
				declared[name.Name] = true
			}
		}
		return true
	})
	if len(declared) < 10 {
		t.Fatalf("found only %d declared sentinels; the scan is not reading types.go", len(declared))
	}

	if got := len(Sentinels()); got != len(declared) {
		t.Fatalf("Sentinels() returns %d errors but %d are declared — a sentinel added to types.go must be added to Sentinels(), or a caller translating them will pass it through as an opaque failure", got, len(declared))
	}
	for _, sentinel := range Sentinels() {
		if sentinel == nil {
			t.Fatal("Sentinels() contains a nil error")
		}
	}
}
