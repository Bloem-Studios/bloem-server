package catalog

// Bloem-owned test. Kept out of Silo's own test files so upstream merges do not conflict here;
// see contracts/seams.txt.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const searchMirrorPrunePinFailure = "Silo changed PruneProcessed; update searchMirrorSiloPruneSQL in bloem_search_mirror_prune.go and re-check the hold"

// TestSearchMirrorPrunePinsSiloQuery keeps searchMirrorSiloPruneSQL a verbatim copy of the query
// in Silo's (*SearchIndexEventRepository).PruneProcessed. Operators with a mirror row run the
// copy, everyone else runs Silo's code, so a Silo change to the query that the copy misses would
// split the two without a sign. The test needs no database: it reads the source.
func TestSearchMirrorPrunePinsSiloQuery(t *testing.T) {
	fail := func(format string, args ...any) {
		t.Helper()
		t.Logf(format, args...)
		t.Fatal(searchMirrorPrunePinFailure)
	}

	file, err := parser.ParseFile(token.NewFileSet(), "search_index_repo.go", nil, parser.SkipObjectResolution)
	if err != nil {
		fail("parse search_index_repo.go: %v", err)
		return
	}

	var prune *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv != nil && fn.Name.Name == "PruneProcessed" && fn.Body != nil {
			prune = fn
			break
		}
	}
	if prune == nil {
		fail("search_index_repo.go has no method PruneProcessed")
		return
	}

	// The query is the first string literal handed to the call that runs it.
	var literal *ast.BasicLit
	ast.Inspect(prune.Body, func(n ast.Node) bool {
		if literal != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "QueryRow", "Query", "Exec":
		default:
			return true
		}
		for _, arg := range call.Args {
			if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				literal = lit
				return false
			}
		}
		return true
	})
	if literal == nil {
		fail("PruneProcessed passes no string literal to QueryRow, Query or Exec")
		return
	}

	silo, err := strconv.Unquote(literal.Value)
	if err != nil {
		fail("unquote the query in PruneProcessed: %v", err)
		return
	}

	got, want := strings.Fields(silo), strings.Fields(searchMirrorSiloPruneSQL)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		fail("PruneProcessed query:\n%s\nsearchMirrorSiloPruneSQL:\n%s", strings.Join(got, " "), strings.Join(want, " "))
	}
}
