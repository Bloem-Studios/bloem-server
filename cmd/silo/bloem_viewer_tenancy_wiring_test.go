package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionViewerResolversUseBloemTenancy keeps Bloem tenancy on in every
// production viewer resolver. policy.NewViewerResolver keeps Silo's
// single-tenant behavior until WithBloemTenancy is applied, so each production
// construction must chain WithBloemTenancy or pass through
// newTenantAwareViewerResolver, which applies it.
func TestProductionViewerResolversUseBloemTenancy(t *testing.T) {
	checkProductionConstructions(t, "policy", "NewViewerResolver", tenancyApplied,
		"policy.NewViewerResolver without WithBloemTenancy or newTenantAwareViewerResolver")
}

// TestProductionProgressSyncServicesUseBloemTenancy keeps snapshot reads on
// the strict tenant path. progresssync.NewService keeps Silo's account-level
// reads until WithBloemTenancy is chained.
func TestProductionProgressSyncServicesUseBloemTenancy(t *testing.T) {
	checkProductionConstructions(t, "progresssync", "NewService", chainsBloemTenancy,
		"progresssync.NewService without WithBloemTenancy")
}

// TestProductionInterestRepositoriesUseBloemTenancy keeps notification fan-out
// candidates bounded by tenant membership and library entitlement.
// notifications.NewInterestRepository keeps Silo's query until WithBloemTenancy
// is chained.
func TestProductionInterestRepositoriesUseBloemTenancy(t *testing.T) {
	checkProductionConstructions(t, "notifications", "NewInterestRepository", chainsBloemTenancy,
		"notifications.NewInterestRepository without WithBloemTenancy")
}

// checkProductionConstructions walks every non-test Go file in the repository
// and reports each pkg.constructor call whose enclosing expression fails
// applied. It fails when it finds no call, so a rename cannot silently pass.
func checkProductionConstructions(t *testing.T, pkg, constructor string, applied func([]ast.Node) bool, message string) {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "web", ".git", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			call, ok := n.(*ast.CallExpr)
			if !ok || !isConstructor(call, file.Name.Name, pkg, constructor) {
				return true
			}
			checked++
			if !applied(stack) {
				t.Errorf("%s: %s", fset.Position(call.Pos()), message)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatalf("found no production %s.%s calls; the walk is broken", pkg, constructor)
	}
}

// isConstructor matches pkg.constructor(...), and constructor(...) inside pkg.
func isConstructor(call *ast.CallExpr, filePkg, pkg, constructor string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return filePkg == pkg && fun.Name == constructor
	case *ast.SelectorExpr:
		ident, ok := fun.X.(*ast.Ident)
		return ok && fun.Sel.Name == constructor && ident.Name == pkg
	}
	return false
}

// chainsBloemTenancy reports whether an enclosing expression of the
// constructor call chains WithBloemTenancy.
func chainsBloemTenancy(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.SelectorExpr:
			if n.Sel.Name == "WithBloemTenancy" {
				return true
			}
		case ast.Stmt, ast.Decl:
			return false
		}
	}
	return false
}

// tenancyApplied reports whether an enclosing expression of the constructor
// call (the innermost last in stack) chains WithBloemTenancy or passes the
// resolver to newTenantAwareViewerResolver.
func tenancyApplied(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.SelectorExpr:
			if n.Sel.Name == "WithBloemTenancy" {
				return true
			}
		case *ast.CallExpr:
			if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "newTenantAwareViewerResolver" {
				return true
			}
		case ast.Stmt, ast.Decl:
			return false
		}
	}
	return false
}
