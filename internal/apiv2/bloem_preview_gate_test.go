package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/Silo-Server/silo-server/contracts/api/v2"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestPreviewGateAllowsListedOperationsOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	serve := func(operationID string, preview bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Header.Set("Authorization", "Bearer preview-token")
		sessions := previewGateSessions{preview: preview}
		am := apimw.NewAuthMiddleware(fakeTokens{map[string]*auth.Claims{"preview-token": {UserID: 1, Role: "user", SessionID: "s1", TokenType: auth.TokenTypeAccess}}}, sessions, nil, nil)
		am.SetPreviewPrincipals(sessions)
		am.SetPreviewRouteGuard(func(*http.Request) bool { return true })
		w := httptest.NewRecorder()
		am.RequireAuth(previewGate(operationID)(next)).ServeHTTP(w, r)
		return w
	}
	if w := serve("getCatalogItem", true); w.Code != http.StatusNoContent {
		t.Fatalf("safe op for preview: %d", w.Code)
	}
	if w := serve("startPlayback", true); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
		t.Fatalf("unlisted op for preview: %d", w.Code)
	}
	if w := serve("startPlayback", false); w.Code != http.StatusNoContent {
		t.Fatalf("unlisted op for a standard account: %d", w.Code)
	}
}

func TestPreviewDenialIsPermissionDeniedWithTheHeader(t *testing.T) {
	d := &denialWriter{header: http.Header{}}
	apimw.WritePreviewOnly(d)
	p := d.problem()
	if !strings.HasSuffix(p.Type, "/"+TypePermissionDenied.ID) || p.headers.Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
		t.Fatalf("problem = %+v headers=%v", p, p.headers)
	}
}

// Every listed operation exists, and a safe one is a read unless it is one of
// the named plumbing writes.
func TestPreviewOperationsAreRealAndSafeOnesRead(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(contract.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{}
	for _, ops := range doc.Paths {
		for method, op := range ops {
			methods[op.OperationID] = strings.ToUpper(method)
		}
	}
	plumbingWrites := map[string]bool{"queryCatalogItems": true, "createEventsSocketTicket": true, "logout": true}
	for id, access := range previewOperations {
		method, ok := methods[id]
		if !ok {
			t.Errorf("preview operation %s is not in the v2 contract", id)
			continue
		}
		if access == previewSafe && method != http.MethodGet && !plumbingWrites[id] {
			t.Errorf("safe preview operation %s is a %s", id, method)
		}
	}
}

type previewGateSessions struct{ preview bool }

func (s previewGateSessions) ActiveSessionRole(context.Context, string) (string, bool, error) {
	return "user", true, nil
}
func (s previewGateSessions) ActiveSessionAuthority(context.Context, string) (string, bool, bool, error) {
	return "user", s.preview, true, nil
}
func (s previewGateSessions) AccountPreviewOnly(context.Context, int) (bool, error) {
	return s.preview, nil
}
