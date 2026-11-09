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
// resolver to newTenantAwareViewerResolver, or whether the statement that
// assigns the resolver is followed at once by Bloem's own line reassigning the
// same variable through withBloemViewerTenancy, as Silo's line in router.go is.
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
		case ast.Stmt:
			if i > 0 {
				if block, ok := stack[i-1].(*ast.BlockStmt); ok {
					return nextStatementAppliesTenancy(block, n)
				}
			}
			return false
		case ast.Decl:
			return false
		}
	}
	return false
}

// nextStatementAppliesTenancy reports whether stmt assigns one variable and
// the statement after it in block assigns that variable again through
// withBloemViewerTenancy (internal/api/router_bloem_wiring.go, cmd/silo/bloem_main.go), which applies
// WithBloemTenancy or panics: v = withBloemViewerTenancy(v, ...).
func nextStatementAppliesTenancy(block *ast.BlockStmt, stmt ast.Stmt) bool {
	name := assignedName(stmt)
	if name == "" {
		return false
	}
	for i, candidate := range block.List {
		if candidate != stmt || i+1 >= len(block.List) {
			continue
		}
		next, ok := block.List[i+1].(*ast.AssignStmt)
		if !ok || assignedName(next) != name {
			return false
		}
		call, ok := next.Rhs[0].(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return false
		}
		if fun, ok := call.Fun.(*ast.Ident); !ok || fun.Name != "withBloemViewerTenancy" {
			return false
		}
		arg, ok := call.Args[0].(*ast.Ident)
		return ok && arg.Name == name
	}
	return false
}

// assignedName returns the variable a single-variable assignment assigns.
func assignedName(stmt ast.Stmt) string {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return ""
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
