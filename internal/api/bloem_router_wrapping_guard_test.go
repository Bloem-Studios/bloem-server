package api

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"testing"
)

// Bloem wraps values Silo's router builds: the viewer gate learns about
// organization revocations, account mail reads the organization's domain
// settings, downloads refuse native storage files, and the v2 services carry
// Bloem's tenancy and native storage. Silo's own lines build the plain value;
// Bloem's lines next to them wrap it. These guards fail when a Silo merge
// moves or rewrites a construction so that Bloem's wrapper no longer applies
// to the value the router finally uses.
func TestRouterAppliesBloemWrappers(t *testing.T) {
	fn := parseRouterFunc(t, "router.go", "newChiRouter")
	for _, rule := range []struct {
		target string // assigned variable or field, or a setter (x.SetY)
		bloem  string // identifier that must appear in the deciding write
		last   bool   // the last write decides; otherwise any write may
	}{
		{"viewerAccessMiddleware", "bloemOrgRevocationAwareViewers", true},
		{"requestSvc.SetLifecycleNotifier", "bloemAdminPushLifecycle", true},
		{"invitationService", "accountMailSettings", false},
		{"passwordResetService", "accountMailSettings", false},
		{"downloadSvc", "GuardNativeStorageFiles", false},
		{"v2deps.EbookFiles", "nativeEbookFiles", true},
		{"v2deps.ThemeSongs", "contentViewer", true},
		{"v2deps.PlaybackControlSocket", "tenantStore", true},
		{"v2deps.EventsSocket", "tenantStore", true},
		{"v2deps.PluginContent", "pluginBoundary", true},
	} {
		writes := writesTo(fn, rule.target)
		if len(writes) == 0 {
			t.Errorf("%s: no write found in newChiRouter; update this guard with the router", rule.target)
			continue
		}
		decided := false
		if rule.last {
			decided = mentions(followCopy(fn, writes[len(writes)-1]), rule.bloem)
		} else {
			for _, w := range writes {
				decided = decided || mentions(w, rule.bloem)
			}
		}
		if !decided {
			t.Errorf("%s: Bloem's %s no longer applies to the value the router uses", rule.target, rule.bloem)
		}
	}
}

func parseRouterFunc(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Recv == nil {
			return fn
		}
	}
	t.Fatalf("%s: no func %s", path, name)
	return nil
}

// writesTo returns, in source order, the statements that assign target, call
// a method on it (target.M(...), also through a type assertion), or call it
// when it names a setter.
func writesTo(fn *ast.FuncDecl, target string) []ast.Stmt {
	var out []ast.Stmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range s.Lhs {
				if exprText(lhs) == target {
					out = append(out, s)
					break
				}
			}
		case *ast.ExprStmt:
			call, ok := s.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			if exprText(call.Fun) == target {
				out = append(out, s)
			} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok && exprText(unassert(sel.X)) == target {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// followCopy follows a plain copy (target = v) to the nearest earlier write
// of v, so socket := build(...); v2deps.X = socket is judged by build(...).
func followCopy(fn *ast.FuncDecl, stmt ast.Stmt) ast.Stmt {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Rhs) != 1 {
		return stmt
	}
	ident, ok := assign.Rhs[0].(*ast.Ident)
	if !ok {
		return stmt
	}
	var source ast.Stmt
	for _, w := range writesTo(fn, ident.Name) {
		if w.Pos() < stmt.Pos() {
			source = w
		}
	}
	if source == nil {
		return stmt
	}
	return source
}

// unassert strips type assertions, so v2deps.X.(*T).Wrap(...) writes v2deps.X.
func unassert(e ast.Expr) ast.Expr {
	for {
		assertion, ok := e.(*ast.TypeAssertExpr)
		if !ok {
			return e
		}
		e = assertion.X
	}
}

func mentions(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

func exprText(e ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), e)
	return buf.String()
}

// The startup guards refuse a value Silo's router no longer builds, rather
// than serving it without Bloem's policy.
func TestRouterWrappersRefuseUnexpectedServices(t *testing.T) {
	for name, wrap := range map[string]func(){
		"theme songs":    func() { bloemThemeSongs(nil, nil) },
		"plugin content": func() { bloemPluginContent(nil, bloemPluginBoundary{}) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic for a service of another type")
				}
			}()
			wrap()
		})
	}
}
