package routeinventory

import (
	"go/ast"
	"go/types"
)

// chiWithReceiver reports the router object a call of the form
// `<router>.With(...)` was taken from, when that receiver is already bound.
// With returns a router over the same mux, so the caller may reuse its scope.
func (a *Analyzer) chiWithReceiver(call *ast.CallExpr, env *walkEnv) (*types.Var, bool) {
	selector, ok := unwrapParen(call.Fun).(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return nil, false
	}
	selection := env.info().Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal || selection.Obj().Name() != "With" {
		return nil, false
	}
	if selection.Obj().Pkg() == nil || selection.Obj().Pkg().Path() != chiImportPath {
		return nil, false
	}
	obj := env.varOf(selector.X)
	if obj == nil || env.routers[obj] == nil {
		return nil, false
	}
	return obj, true
}
