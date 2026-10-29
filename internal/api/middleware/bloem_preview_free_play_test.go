package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// freePlaySettings is a guest settings source that also answers which
// sessions hold a live free-play grant.
type freePlaySettings struct {
	guestSettings
	grants map[string]string
	err    error
}

func (f freePlaySettings) LiveFreePlay(_ context.Context, sessionID string) (string, bool, error) {
	grant, ok := f.grants[sessionID]
	return grant, ok, f.err
}

func serveFreePlayGuest(t *testing.T, source auth.PreviewGuestSettingsSource) (string, bool) {
	t.Helper()
	am := guestMiddleware(auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}, true)
	am.SetPreviewGuests(source, NewPreviewGuestLimiter())
	var grant string
	var marked bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grant, marked = previewcontext.FreePlayGrant(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Host = "browse.example.test"
	r.Header.Set("Authorization", "Bearer guest")
	w := httptest.NewRecorder()
	am.RequireAuth(next).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("guest refused: %d %s", w.Code, w.Body)
	}
	return grant, marked
}

// Only a guest whose session holds a live grant is marked for free play. A
// failed lookup leaves the guest browsing rather than refusing the request.
func TestGuestAdmissionMarksOnlyALiveFreePlayGrant(t *testing.T) {
	settings := guestSettings{auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}}
	if grant, marked := serveFreePlayGuest(t, freePlaySettings{guestSettings: settings, grants: map[string]string{"s-guest": "GrantGrantGrantGrantGr"}}); !marked || grant != "GrantGrantGrantGrantGr" {
		t.Fatalf("live grant: marked=%v grant=%q", marked, grant)
	}
	if _, marked := serveFreePlayGuest(t, freePlaySettings{guestSettings: settings}); marked {
		t.Fatal("no live grant, but marked")
	}
	if _, marked := serveFreePlayGuest(t, freePlaySettings{guestSettings: settings, grants: map[string]string{"s-guest": "GrantGrantGrantGrantGr"}, err: errors.New("database down")}); marked {
		t.Fatal("lookup failed, but marked")
	}
	if _, marked := serveFreePlayGuest(t, settings); marked {
		t.Fatal("source without free play, but marked")
	}
}
