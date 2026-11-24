package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// bloemSocketAuthorityExempt names Silo constructors that build the shared
// socket authority validator but never run it in a contextless upgrade, so
// they need no Bloem tenant wrapper. Each entry carries its reason.
var bloemSocketAuthorityExempt = map[string]string{
	// The validator runs inside the authenticated push-registration request,
	// which already carries the request tenant.
	"NewOrderedApplePushV2": "validates within the authenticated registration request",
}

// Every v2 socket Silo builds checks its single-use ticket in a public
// upgrade that has no request tenant, so Bloem must carry the minting
// request's tenant into it (bloemSocketAuthority). A socket the router wires
// without its Bloem wrapper refuses every connection with 503 "realtime
// authority could not be checked": Watch Together and the administrator log
// stream did exactly that in production until 2026-11-24. This test fails
// when a Silo merge adds a socket, or a constructor that builds the shared
// validator, and the router does not wrap it.
func TestEverySiloSocketCarriesBloemTenantAuthority(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("handlers", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("handlers sources: %v (found %d)", err, len(files))
	}
	socketName := regexp.MustCompile(`^New[A-Za-z0-9]*Socket[A-Za-z0-9]*$`)
	fset := token.NewFileSet()
	constructors := map[string]string{}
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "bloem_") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !fn.Name.IsExported() {
				continue
			}
			buildsValidator := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "newSocketAuthorityValidator" {
						buildsValidator = true
					}
				}
				return !buildsValidator
			})
			if buildsValidator || socketName.MatchString(fn.Name.Name) {
				constructors[fn.Name.Name] = base
			}
		}
	}
	if len(constructors) < 4 {
		t.Fatalf("found %d socket constructors, want at least the events, playback control, Watch Together and log stream sockets: %v", len(constructors), constructors)
	}
	router, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for name, file := range constructors {
		if _, ok := bloemSocketAuthorityExempt[name]; ok {
			continue
		}
		built := strings.Contains(string(router), "handlers."+name+"(")
		wrapper := "handlers.WrapBloem" + strings.TrimPrefix(name, "New") + "("
		if built && !strings.Contains(string(router), wrapper) {
			missing = append(missing, name+" ("+file+"): router.go builds it but never calls "+strings.TrimSuffix(wrapper, "("))
		}
	}
	for name := range bloemSocketAuthorityExempt {
		if _, ok := constructors[name]; !ok {
			missing = append(missing, name+": exempt but no longer found; drop it from bloemSocketAuthorityExempt")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("v2 sockets without Bloem tenant authority (add a WrapBloem…SocketV2 beside bloem_events_socket_v2.go and a Bloem line in router.go):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
