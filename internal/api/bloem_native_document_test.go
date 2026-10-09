package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
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
		if !strings.HasPrefix(r.Path, apiv2.BloemPrefix) {
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
	return out
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
	if len(mounted) != 22 {
		t.Fatalf("native storage mounted operations=%d, want 22", len(mounted))
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
		if !strings.Contains(path, "/native-storage/") {
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

// The conversion is incremental, so most native routes are still undocumented.
// This test does not fail on that -- it reports the remaining count, so the gap
// is a number someone can watch shrink instead of a vague intention. It fails
// only if the count goes UP, which would mean a new chi route was added to the
// native surface without documenting it.
//
// Lower this ceiling as routes are converted. It may only go down.
func TestUndocumentedNativeRouteCountDoesNotGrow(t *testing.T) {
	t.Parallel()
	// 2026-09-14: 99 at introduction (only /capabilities), then 94 (Live TV
	// client operations), 92 (identity and organizations), 84 (watch, sync
	// progress, music), 83 (person detail).
	//
	// October 7: the 30 native-storage paths are documented and walked from
	// their production mount helper. The pinned upstream inventory is retained;
	// its remaining documentation gap does not grow.
	// What remains is admin, plus the Live TV routes beyond the five a viewer
	// calls: DVR recordings, series rules, tuners, guide sources, the session
	// heartbeat and the stream itself.
	const ceiling = 83

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
