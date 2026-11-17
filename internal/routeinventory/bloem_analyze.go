package routeinventory

import (
	"go/ast"
	"go/types"
)

// bloemRouterMatchValue reports whether expr is a bound chi Match method
// value, router.Match, on a router the walk tracks. Bloem's preview route
// guard keeps one (internal/api/router.go). It exposes only a
// boolean lookup, never the router or a registration method, so leakCheck
// lets it go; any other selector on a router still fails closed.
func bloemRouterMatchValue(expr ast.Expr, env *walkEnv) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	selection := env.info().Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal || selection.Obj().Name() != "Match" ||
		selection.Obj().Pkg() == nil || selection.Obj().Pkg().Path() != chiImportPath {
		return false
	}
	obj := env.varOf(selector.X)
	return obj != nil && env.routers[obj] != nil
}

// bloemEnterHelper lets followHelper follow a route registration helper once
// per call rather than once per walk. Bloem mounts one helper at more than one
// prefix (the native storage scope routes, for one), and each call registers
// its own routes, which the inventory records under each prefix. Following a
// helper that is already on the walk's stack would never end, so that stays
// an error. enteredFuncs keeps Silo's meaning, every helper ever entered, for
// the orphan audit and the sweep: the mark is lifted here only for Silo's
// visit-once check that follows, which sets it again.
func (a *Analyzer) bloemEnterHelper(call *ast.CallExpr, decl *ast.FuncDecl) error {
	if a.bloemHelperStack == nil {
		a.bloemHelperStack = map[*ast.FuncDecl]bool{}
	}
	if a.bloemHelperStack[decl] {
		return a.errorf(call, "route registration helper %s is recursive; "+
			"the inventory cannot enumerate a cycle", decl.Name.Name)
	}
	a.bloemHelperStack[decl] = true
	delete(a.enteredFuncs, decl)
	return nil
}

// bloemLeaveHelper takes decl off the walk's stack when followHelper returns.
func (a *Analyzer) bloemLeaveHelper(decl *ast.FuncDecl) {
	delete(a.bloemHelperStack, decl)
}
