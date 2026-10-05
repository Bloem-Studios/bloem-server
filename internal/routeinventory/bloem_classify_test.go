package routeinventory

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
	"slices"
	"testing"
)

func TestBloemLiveTVDeliveryProfileAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{
		"bloemLiveTVStreamTokens(s.auth, s.liveTV.JWTSecret)",
		"s.auth.RequireAuth",
		"s.viewer.RequireViewerAccess",
		"requireBloemLiveTVProfile",
	})
	if class != authProfileScoped {
		t.Fatalf("auth class = %q, want %q", class, authProfileScoped)
	}
	for _, trait := range []string{traitAuthenticated, traitViewerAccess, traitProfileReq, traitStreamToken} {
		if !slices.Contains(traits, trait) {
			t.Errorf("missing %q in %v", trait, traits)
		}
	}
	if slices.Contains(traits, "unclassified_middleware") {
		t.Errorf("owned delivery gates are unclassified: %v", traits)
	}
}

func TestBloemPlatformEngagementAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{"adminMW.Require", "handlers.RequireBloemPlatformContext"})
	if class != authActingAdmin {
		t.Fatalf("auth class = %q, want %q", class, authActingAdmin)
	}
	if !slices.Equal(traits, []string{traitActingAdmin, traitAdminContext}) {
		t.Fatalf("platform context traits = %v", traits)
	}
}

func TestBloemSeasonalViewerAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{
		"client.auth.RequireAuth", "client.tenant.ResolveNative",
		"client.rateLimit.Handler", "client.viewer.RequireViewerAccess", "apimw.RequireProfile",
	})
	if class != authProfileScoped || !slices.Equal(traits, []string{
		traitAuthenticated, traitProfileReq, traitRateLimited, traitTenantScoped, traitViewerAccess,
	}) {
		t.Fatalf("seasonal viewer authority = %q %v", class, traits)
	}
}

func TestBloemProfileCredentialRateLimitClassification(t *testing.T) {
	class, traits := classifyAuth([]string{"authMW.RequireAuth", "surfaces.ProfileCredentialLimit"})
	if class != authAuthenticated || !slices.Equal(traits, []string{traitAuthenticated, traitRateLimited}) {
		t.Fatalf("profile credential authority = %q %v", class, traits)
	}
}

func TestBloemAccessGroupWrapperPreservesLeafAndProvenance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		resolved   bool
		streams    bool
		shape      string
	}{
		{"direct", "hg.RequireAccessGroupTenant(h.HandleList)", true, false, ""},
		{"parenthesized", "(hg.RequireAccessGroupTenant((h.HandleList)))", true, false, ""},
		{"conversion", "http.HandlerFunc(hg.RequireAccessGroupTenant(h.HandleList))", true, false, ""},
		{"nested_parenthesized_conversions", "http.HandlerFunc((http.HandlerFunc(hg.RequireAccessGroupTenant(h.HandleList))))", true, false, ""},
		{"nested_parenthesized_conversion_stream", "observeNative((http.HandlerFunc((http.HandlerFunc(hg.RequireAccessGroupTenant(h.HandleList))))))", true, true, ""},
		{"nested_parenthesized_observers", "observeNative((observeProxy(hg.RequireAccessGroupTenant(h.HandleList))))", true, true, ""},
		{"nested_parenthesized_foreign_conversion", "http.HandlerFunc((http.HandlerFunc(other.RequireAccessGroupTenant(h.HandleList))))", false, false, ""},
		{"stream_outside", "observeNative(hg.RequireAccessGroupTenant(h.HandleList))", true, true, ""},
		{"stream_inside", "hg.RequireAccessGroupTenant(observeProxy(h.HandleList))", true, true, ""},
		{"nested_conversion_stream", "observeNode(http.HandlerFunc((hg.RequireAccessGroupTenant(observeNative(h.HandleList)))))", true, true, ""},
		{"foreign_package", "other.RequireAccessGroupTenant(h.HandleList)", false, false, ""},
		{"method", "g.RequireAccessGroupTenant(h.HandleList)", false, false, ""},
		{"unknown", "unknown(h.HandleList)", false, false, ""},
		{"function_value", "guard(h.HandleList)", false, false, ""},
		{"missing_types", "hg.RequireAccessGroupTenant(h.HandleList)", false, false, "missing_types"},
		{"untyped", "hg.RequireAccessGroupTenant(h.HandleList)", false, false, "untyped"},
		{"multiple_arguments", "hg.RequireAccessGroupTenant(h.HandleList)", false, false, "multiple_arguments"},
		{"no_arguments", "hg.RequireAccessGroupTenant(h.HandleList)", false, false, "no_arguments"},
		{"ellipsis", "hg.RequireAccessGroupTenant(h.HandleList)", false, false, "ellipsis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expr, info, fset := bloemWrapperFixture(t, tc.expression)
			switch tc.shape {
			case "missing_types":
				info = nil
			case "untyped":
				info = &types.Info{Uses: map[*ast.Ident]types.Object{}}
			case "multiple_arguments":
				call := expr.(*ast.CallExpr)
				call.Args = append(call.Args, call.Args[0])
			case "no_arguments":
				expr.(*ast.CallExpr).Args = nil
			case "ellipsis":
				expr.(*ast.CallExpr).Ellipsis = expr.Pos()
			}
			set := &sourceSet{fset: fset, modulePath: "github.com/Silo-Server/silo-server"}
			env := &walkEnv{pkg: &pkgSource{Pkg: &packages.Package{TypesInfo: info}}, listener: ListenerSpec{ID: ListenerAPI}}
			got := newClassifier(set).describe(expr, "GET", "/groups", env)
			if got.expr != set.exprText(expr) {
				t.Fatalf("registration provenance lost: %q, want %q", got.expr, set.exprText(expr))
			}
			if got.resolved != tc.resolved || got.streams != tc.streams {
				t.Fatalf("resolved/streams = %v/%v, want %v/%v; identity=%q", got.resolved, got.streams, tc.resolved, tc.streams, got.identity)
			}
			if tc.resolved && (got.kind != handlerKindMethod || got.identity != "(*internal/api/handlers.AccessGroupHandler).HandleList") {
				t.Fatalf("leaf identity/kind = %q/%q", got.identity, got.kind)
			}
			if !tc.resolved && got.kind != handlerKindExpression {
				t.Fatalf("untrusted wrapper kind = %q, want expression", got.kind)
			}
		})
	}
}

type bloemWrapperImporter map[string]*types.Package

func (i bloemWrapperImporter) Import(path string) (*types.Package, error) {
	if pkg := i[path]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("unexpected fixture import %q", path)
}

// Type-check a package import alias against actual function symbols. These
// small in-memory packages isolate symbol resolution from unrelated server
// wiring while exercising describe's real type-checker boundary.
func bloemWrapperFixture(t *testing.T, expression string) (ast.Expr, *types.Info, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	imports := bloemWrapperImporter{}
	for _, p := range []struct{ path, source string }{
		{"github.com/Silo-Server/silo-server/internal/api/handlers", `package handlers
func RequireAccessGroupTenant(next func()) func() { return next }
type AccessGroupHandler struct{}
func (*AccessGroupHandler) HandleList() {}
type Guard struct{}
func (Guard) RequireAccessGroupTenant(next func()) func() { return next }
`},
		{"example.test/foreign", `package foreign
func RequireAccessGroupTenant(next func()) func() { return next }
`},
		{"net/http", `package http
type HandlerFunc func()
`},
	} {
		file, err := parser.ParseFile(fset, p.path+".go", p.source, 0)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := new(types.Config).Check(p.path, fset, []*ast.File{file}, nil)
		if err != nil {
			t.Fatal(err)
		}
		imports[p.path] = pkg
	}
	source := `package listener
import hg "github.com/Silo-Server/silo-server/internal/api/handlers"
import other "example.test/foreign"
import "net/http"
var h hg.AccessGroupHandler
var g hg.Guard
var guard = hg.RequireAccessGroupTenant
var _ = other.RequireAccessGroupTenant(h.HandleList)
var _ http.HandlerFunc
func observeNative(next func()) func() { return next }
func observeProxy(next func()) func() { return next }
func observeNode(next func()) func() { return next }
func unknown(next func()) func() { return next }
var wrapped = ` + expression
	file, err := parser.ParseFile(fset, "wrapper.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
		Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	config := types.Config{Importer: imports}
	if _, err := config.Check("example.test/listener", fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if general, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range general.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok && value.Names[0].Name == "wrapped" {
					return value.Values[0], info, fset
				}
			}
		}
	}
	t.Fatal("fixture has no wrapped value")
	return nil, nil, nil
}
