package apiv2

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// TestPlaybackDeliveryAccess* drives the real handlers.StreamHandler (not a
// fake) through the v2 stream route with a viewer scope this test controls,
// proving the byte-delivery path rechecks library membership on every
// request rather than trusting the session or a signed restart recipe. A
// session or recipe card names a source; it does not preserve an
// entitlement — see internal/api/handlers/playback_library_access.go.

// deliveryAccessFiles is a minimal handlers.FilePathResolver keyed by file ID.
type deliveryAccessFiles map[int]*models.MediaFile

func (r deliveryAccessFiles) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	if f, ok := r[id]; ok {
		return f, nil
	}
	return nil, errors.New("media file not found")
}

// deliveryAccessMediaFile writes a real backing file so a permitted request
// can be served all the way through preflightPlaybackFile's os.Stat check.
func deliveryAccessMediaFile(t *testing.T, id, libraryID int) *models.MediaFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatalf("write test media file: %v", err)
	}
	return &models.MediaFile{ID: id, MediaFolderID: libraryID, FilePath: path}
}

// deliveryAccessDeps wires the real v1 StreamHandler behind the v2 /stream
// route, gated by a mutable viewer scope (policyResolver, from progress_test.go).
func deliveryAccessDeps(policy *access.Scope, files deliveryAccessFiles) (Dependencies, *playback.SessionManager, *handlers.StreamHandler) {
	deps := parityDeps(false)
	deps.ViewerAccess = apimw.NewViewerAccessMiddleware(policyResolver{scope: policy})
	sessionMgr := playback.NewSessionManager(0, 0)
	stream := handlers.NewStreamHandler(sessionMgr, files)
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(stream.HandleStream)}
	return deps, sessionMgr, stream
}

// TestPlaybackDeliveryAccessIsPerFileNotPerTitle proves the recheck is keyed
// on the file's own library membership, not on some coarser catalog-title
// grouping: two files that could belong to the same title get opposite
// answers because they sit in different libraries.
func TestPlaybackDeliveryAccessIsPerFileNotPerTitle(t *testing.T) {
	allowedFile := deliveryAccessMediaFile(t, 101, 1)
	blockedFile := deliveryAccessMediaFile(t, 102, 2)
	files := deliveryAccessFiles{101: allowedFile, 102: blockedFile}
	policy := &access.Scope{AllowedLibraryIDs: []int{1}, LibrariesRestricted: true}
	deps, sessionMgr, _ := deliveryAccessDeps(policy, files)
	h := newTestHandler(t, deps)

	allowedSession, err := sessionMgr.StartSession(1, "", 101, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start allowed session: %v", err)
	}
	blockedSession, err := sessionMgr.StartSession(1, "", 102, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start blocked session: %v", err)
	}

	if rec := do(t, h, http.MethodGet, Prefix+"/stream/"+allowedSession.ID, "", bearer(memberToken)); rec.Code != 200 || rec.Body.String() != "video" {
		t.Fatalf("file inside scope: %d %s", rec.Code, rec.Body.String())
	}
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+blockedSession.ID, "", bearer(memberToken)), TypeNotFound)
}

// TestPlaybackDeliveryAccessPrivateVsSharedLibrary covers a private library
// scope (one library allowed) and a shared one (several libraries allowed):
// in both shapes a file outside the allowed set is refused and a file inside
// it is served.
func TestPlaybackDeliveryAccessPrivateVsSharedLibrary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scope  []int
		inside int
	}{
		{"private library scope", []int{5}, 5},
		{"shared library scope", []int{5, 6, 7}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			insideFile := deliveryAccessMediaFile(t, 201, tc.inside)
			outsideFile := deliveryAccessMediaFile(t, 202, 999)
			files := deliveryAccessFiles{201: insideFile, 202: outsideFile}
			policy := &access.Scope{AllowedLibraryIDs: tc.scope, LibrariesRestricted: true}
			deps, sessionMgr, _ := deliveryAccessDeps(policy, files)
			h := newTestHandler(t, deps)

			insideSession, err := sessionMgr.StartSession(1, "", 201, playback.PlayDirect, false)
			if err != nil {
				t.Fatalf("start inside session: %v", err)
			}
			outsideSession, err := sessionMgr.StartSession(1, "", 202, playback.PlayDirect, false)
			if err != nil {
				t.Fatalf("start outside session: %v", err)
			}

			if rec := do(t, h, http.MethodGet, Prefix+"/stream/"+insideSession.ID, "", bearer(memberToken)); rec.Code != 200 || rec.Body.String() != "video" {
				t.Fatalf("library inside scope: %d %s", rec.Code, rec.Body.String())
			}
			requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+outsideSession.ID, "", bearer(memberToken)), TypeNotFound)
		})
	}
}

// TestPlaybackDeliveryAccessEntitlementRevokedBetweenRequests proves the
// check is live, not cached at session start: the same session serves bytes
// while its library is allowed and is refused the moment the policy revokes
// that library, with no new session and no client-side change at all.
func TestPlaybackDeliveryAccessEntitlementRevokedBetweenRequests(t *testing.T) {
	file := deliveryAccessMediaFile(t, 301, 9)
	files := deliveryAccessFiles{301: file}
	policy := &access.Scope{AllowedLibraryIDs: []int{9}, LibrariesRestricted: true}
	deps, sessionMgr, _ := deliveryAccessDeps(policy, files)
	h := newTestHandler(t, deps)

	session, err := sessionMgr.StartSession(1, "", 301, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	path := Prefix + "/stream/" + session.ID

	if rec := do(t, h, http.MethodGet, path, "", bearer(memberToken)); rec.Code != 200 || rec.Body.String() != "video" {
		t.Fatalf("first request (entitled): %d %s", rec.Code, rec.Body.String())
	}

	// The entitlement is removed: the library drops out of the allowlist.
	policy.AllowedLibraryIDs = []int{}

	requireProblem(t, do(t, h, http.MethodGet, path, "", bearer(memberToken)), TypeNotFound)
}

// TestPlaybackDeliveryAccessLiveVersusReconstructedSession proves the recheck
// runs identically for a live in-memory session and for one rebuilt from a
// signed restart recipe card (e.g. after a server restart): the same
// library-scope answer applies to both delivery shapes.
func TestPlaybackDeliveryAccessLiveVersusReconstructedSession(t *testing.T) {
	const secret = "delivery-access-test-secret"
	allowedFile := deliveryAccessMediaFile(t, 401, 11)
	blockedFile := deliveryAccessMediaFile(t, 402, 12)
	files := deliveryAccessFiles{401: allowedFile, 402: blockedFile}
	policy := &access.Scope{AllowedLibraryIDs: []int{11}, LibrariesRestricted: true}
	deps, sessionMgr, stream := deliveryAccessDeps(policy, files)
	stream.JWTSecret = secret
	h := newTestHandler(t, deps)

	// Live session, allowed library: served normally with no token.
	liveAllowed, err := sessionMgr.StartSession(1, "", 401, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start live allowed session: %v", err)
	}
	if rec := do(t, h, http.MethodGet, Prefix+"/stream/"+liveAllowed.ID, "", bearer(memberToken)); rec.Code != 200 || rec.Body.String() != "video" {
		t.Fatalf("live session, allowed library: %d %s", rec.Code, rec.Body.String())
	}

	// Live session, blocked library: refused with no token.
	liveBlocked, err := sessionMgr.StartSession(1, "", 402, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start live blocked session: %v", err)
	}
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+liveBlocked.ID, "", bearer(memberToken)), TypeNotFound)

	// Reconstructed session (fresh SessionManager, as after a restart): the
	// live in-memory entry is gone and only the signed recipe card names the
	// source, but the guard still recovers the file before serving it.
	freshSessionMgr := playback.NewSessionManager(0, 0)
	freshStream := handlers.NewStreamHandler(freshSessionMgr, files)
	freshStream.JWTSecret = secret
	// A bare TranscodeManager (the StreamHandler default) has no recipe store
	// and never reconstructs; wiring one up is what the production router does
	// to enable restart recovery, and it is what makes this a genuine
	// reconstruct-from-recipe case rather than a live in-memory session.
	freshStream.TM.Sessions = freshSessionMgr
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(freshStream.HandleStream)}
	freshHandler := newTestHandler(t, deps)

	allowedCard := playback.NewDirectRecipeCard("11111111-1111-4111-8111-111111111112", 1, "", 401)
	allowedToken, err := streamtoken.Sign(allowedCard.ToClaims(), secret, time.Hour)
	if err != nil {
		t.Fatalf("sign allowed card: %v", err)
	}
	if rec := do(t, freshHandler, http.MethodGet, Prefix+"/stream/"+allowedCard.SessionID+"?st="+allowedToken, "", bearer(memberToken)); rec.Code != 200 || rec.Body.String() != "video" {
		t.Fatalf("reconstructed session, allowed library: %d %s", rec.Code, rec.Body.String())
	}

	blockedCard := playback.NewDirectRecipeCard("22222222-2222-4222-8222-222222222223", 1, "", 402)
	blockedToken, err := streamtoken.Sign(blockedCard.ToClaims(), secret, time.Hour)
	if err != nil {
		t.Fatalf("sign blocked card: %v", err)
	}
	requireProblem(t, do(t, freshHandler, http.MethodGet, Prefix+"/stream/"+blockedCard.SessionID+"?st="+blockedToken, "", bearer(memberToken)), TypeNotFound)
}
