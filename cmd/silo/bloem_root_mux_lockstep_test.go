package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// The route inventory keys ListenerRoot on the literal name newRootMux, so it
// walks upstream's function in root_handler.go and never sees bloemRootMux --
// the mux Bloem's binary actually serves. That is only safe while the two
// register the same patterns. If bloemRootMux gains one upstream's does not,
// the generator cannot see it and inventory coverage shrinks with nothing
// failing.
//
// bloem_root_handler.go's own comment asks for that lockstep and, until this
// test, nothing enforced it. Enforcing it in source rather than at runtime is
// deliberate: http.ServeMux exposes no way to enumerate what was registered on
// it, and the inventory's own guarantee is a source-level one.
func TestBloemRootMuxStaysInLockstepWithUpstream(t *testing.T) {
	t.Parallel()

	upstream := muxPatterns(t, "root_handler.go", "newRootMux")
	bloem := muxPatterns(t, "bloem_root_handler.go", "bloemRootMux")

	if len(upstream) == 0 {
		t.Fatal("no patterns found in newRootMux; the extraction is broken, not the lockstep")
	}
	if len(bloem) != len(upstream) {
		t.Fatalf("pattern count differs: newRootMux registers %v, bloemRootMux registers %v", upstream, bloem)
	}
	for i := range upstream {
		if bloem[i] != upstream[i] {
			t.Errorf("pattern %d differs: newRootMux has %q, bloemRootMux has %q\n"+
				"The route inventory walks newRootMux and attributes its coverage to the mux Bloem serves. "+
				"Keep both registering the same patterns in the same order, or give bloemRootMux its own "+
				"listener entry in internal/routeinventory/config.go.",
				i, upstream[i], bloem[i])
		}
	}
}

// muxPatterns returns, in registration order, the literal patterns the named
// function passes to mux.Handle. A non-literal pattern fails the test rather
// than being skipped: an unreadable registration is exactly the case where the
// two functions could drift unnoticed.
func muxPatterns(t *testing.T, file, fn string) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(file), nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	var decl *ast.FuncDecl
	for _, node := range parsed.Decls {
		if fd, ok := node.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == fn {
			decl = fd
			break
		}
	}
	if decl == nil || decl.Body == nil {
		t.Fatalf("%s not found in %s", fn, file)
	}

	var patterns []string
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || selector.Sel.Name != "Handle" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Errorf("%s: mux.Handle called with a non-literal pattern; the lockstep check cannot read it", fn)
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Errorf("%s: unquoting pattern %s: %v", fn, literal.Value, err)
			return true
		}
		patterns = append(patterns, value)
		return true
	})
	return patterns
}
