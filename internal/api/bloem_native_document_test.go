package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
)

// The native surface's document is only worth having if it describes the
// surface that is actually mounted. These tests hold it to that in both
// directions: nothing documented may be absent from the router, and the routes
// not yet documented are counted so the gap is visible rather than assumed.

func generatedBloemPaths(t *testing.T) map[string]struct{} {
	t.Helper()
	raw, err := apiv2.GenerateBloemOpenAPI()
	if err != nil {
		t.Fatalf("generating the native document: %v", err)
	}
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the native document: %v", err)
	}
	out := make(map[string]struct{}, len(doc.Paths))
	for p := range doc.Paths {
		out[p] = struct{}{}
	}
	return out
}

// mountedBloemPaths preserves the committed upstream inventory checks and adds
// the native-storage group by walking its actual production mount helper. The
// upstream snapshot predates this optional Bloem group; it is not rewritten or
// treated as proof that the group is absent. Keeping this test in package api
// reaches the real registrar without exporting a runtime router-recovery hook.
func mountedBloemPaths(t *testing.T) map[string]struct{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "api", "v2", "route-inventory.json"))
	if err != nil {
		t.Skipf("route inventory unavailable (%v); run `make route-inventory`", err)
	}
	var inv struct {
		Routes []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatalf("parsing the route inventory: %v", err)
	}
	out := map[string]struct{}{}
	for _, r := range inv.Routes {
		if !strings.HasPrefix(r.Path, apiv2.BloemPrefix) || strings.HasSuffix(r.Path, "/*") {
			continue
		}
		switch r.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			out[r.Path] = struct{}{}
		}
	}
	for operation := range mountedNativeStorageOperations(t) {
		_, path, _ := strings.Cut(operation, " ")
		out[path] = struct{}{}
	}
	for operation := range mountedBookRequestOperations(t) {
		_, path, _ := strings.Cut(operation, " ")
		out[path] = struct{}{}
	}
	return out
}

// Walk the same conditional registrars as production, with all authority
// dependencies present. Fixtures that omit these dependencies hide the viewer
// routes even though the server mounts them for an authenticated profile.
func mountedBookRequestOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	service := bookrequests.NewService(nil, nil)
	h := handlers.NewBloemBookRequestsHandler(service, seasonalMountProfiles{})
	h.SetEbookRequests(service)
	client := bloemClientSurface{
		auth:   apimw.NewAuthMiddleware(seasonalMountTokens{}, seasonalMountSessions{}, nil, nil),
		tenant: apimw.NewTenantMiddleware(seasonalMountTenant{}),
		viewer: apimw.NewViewerAccessMiddleware(seasonalMountViewer{}),
	}
	router := chi.NewRouter()
	router.Route(apiv2.BloemPrefix, func(r chi.Router) {
		mountBloemBookRequests(r, h, client, nil)
		r.Route("/admin", func(r chi.Router) { mountBloemAdminBookRequests(r, h, nil) })
	})
	operations := map[string]struct{}{}
	if err := chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		operations[method+" "+path] = struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return operations
}

func TestBookRequestDocumentMatchesMountedOperations(t *testing.T) {
	mounted := mountedBookRequestOperations(t)
	if len(mounted) != 33 {
		t.Fatalf("book request operations=%d, want 33", len(mounted))
	}
	raw, err := apiv2.GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]struct{}{}
	for path, item := range doc.Paths {
		if !strings.Contains(path, "/book-requests") && !strings.Contains(path, "/ebook-request") && !strings.Contains(path, "/audiobook-request") {
			continue
		}
		for method := range item {
			switch method {
			case "get", "post", "put", "delete", "patch", "head", "options", "trace":
				documented[strings.ToUpper(method)+" "+path] = struct{}{}
			}
		}
	}
	for operation := range mounted {
		if _, ok := documented[operation]; !ok {
			t.Errorf("mounted book request operation absent from OpenAPI: %s", operation)
		}
	}
	for operation := range documented {
		if _, ok := mounted[operation]; !ok {
			t.Errorf("documented book request operation not mounted: %s", operation)
		}
	}
}

func mountedNativeStorageOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	router := chi.NewRouter()
	router.Route(apiv2.BloemPrefix+"/admin", func(r chi.Router) {
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil))
	})
	operations := map[string]struct{}{}
	if err := chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		operations[method+" "+path] = struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return operations
}

// A method change or scope omission must fail even if the path still exists.
func TestNativeStorageDocumentMatchesMountedOperations(t *testing.T) {
	mounted := mountedNativeStorageOperations(t)
	if len(mounted) != 40 {
		t.Fatalf("native storage mounted operations=%d, want 40", len(mounted))
	}
	raw, err := apiv2.GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	documented := map[string]struct{}{}
	for path, item := range document.Paths {
		if !strings.Contains(path, "/native-storage/") && !strings.Contains(path, "/library-locations") {
			continue
		}
		for method := range item {
			switch method {
			case "get", "post", "put", "delete", "patch", "head", "options", "trace":
				documented[strings.ToUpper(method)+" "+path] = struct{}{}
			}
		}
	}
	for operation := range mounted {
		if _, ok := documented[operation]; !ok {
			t.Errorf("mounted native operation absent from OpenAPI: %s", operation)
		}
	}
	for operation := range documented {
		if _, ok := mounted[operation]; !ok {
			t.Errorf("documented native operation not mounted: %s", operation)
		}
	}
}

// A documented path the router does not mount is a promise to clients that the
// server does not keep. That is worse than an undocumented route, because a
// client generator will produce code for it.
func TestEveryDocumentedNativePathIsMounted(t *testing.T) {
	t.Parallel()
	mounted := mountedBloemPaths(t)
	if len(mounted) == 0 {
		t.Skip("no native routes in the inventory")
	}
	for path := range generatedBloemPaths(t) {
		if _, ok := mounted[path]; !ok {
			t.Errorf("the native document promises %s, which the router does not mount", path)
		}
	}
}

// Every documented path must carry the native prefix. Registering a native
// operation at a v2 path would put it in the wrong document and serve it from
// the wrong surface.
func TestEveryDocumentedNativePathCarriesTheNativePrefix(t *testing.T) {
	t.Parallel()
	for path := range generatedBloemPaths(t) {
		if !strings.HasPrefix(path, apiv2.BloemPrefix) {
			t.Errorf("%s is in the native document but is not under %s", path, apiv2.BloemPrefix)
		}
	}
}

// Every explicit native inventory path must have a typed declaration.
func TestUndocumentedNativeRouteCountDoesNotGrow(t *testing.T) {
	t.Parallel()
	const ceiling = 0

	mounted := mountedBloemPaths(t)
	if len(mounted) == 0 {
		t.Skip("no native routes in the inventory")
	}
	documented := generatedBloemPaths(t)

	var missing []string
	for path := range mounted {
		if _, ok := documented[path]; !ok {
			missing = append(missing, path)
		}
	}
	if len(missing) > ceiling {
		t.Errorf("undocumented native routes rose to %d, above the ceiling of %d; "+
			"a route was added to the native surface without a huma registration.\n"+
			"Document it in registerBloemAll, or lower the ceiling if you converted routes.",
			len(missing), ceiling)
	}
	t.Logf("native routes: %d mounted, %d documented, %d still to convert",
		len(mounted), len(documented), len(missing))
}

// Exercise production registrars with every administration dependency present.
// chi Handle advertises all verbs; probe its OPTIONS handler without middleware
// to recover methodOnly's actual Allow contract rather than inventing operations.
func mountedAdministrativeAndLiveTVOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), &handlers.AdminContextSessionHandler{},
		&apimw.AuthMiddleware{}, &apimw.AdminContextMiddleware{}, bloemRouteSurfaces{
			Platform: &handlers.BloemAdminPlatformHandler{}, People: &handlers.BloemAdminPeopleHandler{},
			Organization: &handlers.BloemAdminOrganizationHandler{}, Explain: &handlers.BloemPolicyExplainHandler{},
			Promotions: &handlers.AdminPromotionsHandler{}, Ambience: &handlers.AmbienceHandler{},
			Entitlement: &handlers.EntitlementTemplatesHandler{}, AccountPolicy: &handlers.AdminHandler{},
			PreviewAdmin: &handlers.BloemPreviewAdminHandler{},
			Client:       bloemClientSurface{auth: &apimw.AuthMiddleware{}, tenant: &apimw.TenantMiddleware{}, viewer: &apimw.ViewerAccessMiddleware{}, liveTV: &handlers.LiveTVHandler{}, liveTVAdmin: func(h http.Handler) http.Handler { return h }},
		})
	operations := map[string]struct{}{}
	gated := map[string]string{}
	if err := chi.Walk(router, func(method, path string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasSuffix(path, "/*") || (!strings.Contains(path, "/admin/") && !strings.Contains(path, "/livetv/")) {
			return nil
		}
		if strings.Contains(path, "/native-storage/") || strings.Contains(path, "/library-locations") || (strings.Contains(path, "/book-requests") || strings.Contains(path, "/ebook-request") || strings.Contains(path, "/audiobook-request")) {
			return nil
		}
		if method == http.MethodOptions {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodOptions, path, nil))
			if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") == "" {
				t.Fatalf("unresolved dispatcher %s: status=%d Allow=%q", path, response.Code, response.Header().Get("Allow"))
			}
			gated[path] = response.Header().Get("Allow")
		}
		operations[method+" "+path] = struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for path, allowed := range gated {
		for operation := range operations {
			_, p, _ := strings.Cut(operation, " ")
			if p == path {
				delete(operations, operation)
			}
		}
		operations[allowed+" "+path] = struct{}{}
	}
	return operations
}

func TestAdministrativeAndLiveTVDocumentMatchesMountedOperations(t *testing.T) {
	mounted := mountedAdministrativeAndLiveTVOperations(t)
	raw, err := apiv2.GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]struct{}{}
	for path, item := range doc.Paths {
		if !strings.Contains(path, "/livetv/") && !strings.Contains(path, "/admin/organization/") && !strings.Contains(path, "/admin/platform/") && !strings.HasSuffix(path, "/admin/session") {
			continue
		}
		// Other admin families have their own production-mount tests.
		if strings.Contains(path, "/native-storage/") || strings.Contains(path, "/library-locations") || (strings.Contains(path, "/book-requests") || strings.Contains(path, "/ebook-request") || strings.Contains(path, "/audiobook-request")) {
			continue
		}
		for method := range item {
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
				documented[strings.ToUpper(method)+" "+path] = struct{}{}
			}
		}
	}
	for operation := range mounted {
		if _, ok := documented[operation]; !ok {
			t.Errorf("mounted operation missing typed OpenAPI: %s", operation)
		}
	}
	for operation := range documented {
		if _, ok := mounted[operation]; !ok {
			t.Errorf("documented operation is not mounted: %s", operation)
		}
	}
	t.Logf("administration and Live TV: %d mounted operations, %d documented", len(mounted), len(documented))
}
