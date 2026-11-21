package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// A replan plans from the attempt's start request narrowed by the remote
// plan overrides an administrator pinned (remote_control.go). Silo's
// executeReplanV3 starts from record.NormalizedRequest; Bloem's line right
// after it applies the overrides. This guard fails when a Silo change moves
// that start so the overrides no longer apply before the replan reads it.
func TestReplanStartsFromRemotePlanOverrides(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "playback_v3.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body []ast.Stmt
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "executeReplanV3" {
			body = fn.Body.List
		}
	}
	if body == nil {
		t.Fatal("playback_v3.go: no executeReplanV3; update this guard with Silo's replan")
	}
	for i, stmt := range body {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 {
			continue
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); !ok || ident.Name != "start" {
			continue
		}
		if callsRemotePlanOverrides(assign) {
			return
		}
		if i+1 < len(body) {
			if next, ok := body[i+1].(*ast.AssignStmt); ok && len(next.Lhs) == 1 && callsRemotePlanOverrides(next) {
				if ident, ok := next.Lhs[0].(*ast.Ident); ok && ident.Name == "start" {
					return
				}
			}
		}
		t.Fatal("executeReplanV3: start is not narrowed by applyRemotePlanOverridesV3 where it is declared")
	}
	t.Fatal("executeReplanV3: no start := declaration")
}

func callsRemotePlanOverrides(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "applyRemotePlanOverridesV3" {
			found = true
		}
		return !found
	})
	return found
}
