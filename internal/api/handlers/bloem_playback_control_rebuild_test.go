package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

const controlRebuildSecret = "control-rebuild-secret"

// newControlRebuildFixture is the Bloem control socket on an API process that
// does not hold a live session, as after a blue/green switch: the attempt is
// in the shared store, the new process's session manager is empty.
func newControlRebuildFixture(t *testing.T) (*bloemControlSocketFixture, string) {
	t.Helper()
	f := newBloemControlSocketFixture(t)
	f.pb.JWTSecret = controlRebuildSecret
	return f, uuid.NewString()
}

func (f *bloemControlSocketFixture) saveLiveAttempt(t *testing.T, sessionID, planID, streamURL string, features ...string) {
	t.Helper()
	identity := f.authority.identity
	record := playback.AttemptRecordV3{PlaybackAttemptID: "attempt-" + sessionID, SessionID: sessionID, UserID: identity.UserID, ProfileID: identity.ProfileID,
		CurrentPlanID: planID, ExpiresAt: time.Now().Add(time.Hour), NormalizedRequest: playback.StartRequestV3{ClientFeatures: features}}
	record.CurrentPlan.Stream.URL = streamURL
	if err := f.pb.PlanStoreV3.SaveAttempt(t.Context(), record); err != nil {
		t.Fatal(err)
	}
}

func (f *bloemControlSocketFixture) apiDirectCard(sessionID string) playback.RecipeCard {
	card := playback.NewDirectRecipeCard(sessionID, f.authority.identity.UserID, f.authority.identity.ProfileID, 100)
	card.RoutingWorkload = string(noderouting.WorkloadDirectPlay)
	card.RoutingExecution = string(noderouting.ExecutionNone)
	card.RoutingEgress = string(noderouting.EgressAPI)
	return card
}

// planStreamURL is the stream URL a v3 plan hands a token-authenticated
// player: the media route plus the signed recipe in st.
func planStreamURL(t *testing.T, card playback.RecipeCard, secret string) string {
	t.Helper()
	token, err := streamtoken.Sign(card.ToClaims(), secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return "/stream/" + card.SessionID + "?" + url.Values{streamTokenParam: {token}}.Encode()
}

// After an API process switch the player keeps reporting progress and asks
// for a control ticket every Retry-After, but sends no media request while its
// buffer lasts. The ticket itself must bring the session back, from the token
// the player's next media request would carry, so the lane opens now.
func TestBloemControlMintRebuildsALiveSessionAfterAProcessSwitch(t *testing.T) {
	f, sessionID := newControlRebuildFixture(t)
	f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(sessionID), controlRebuildSecret))
	if _, err := f.manager.GetSession(sessionID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("precondition: this process must not hold the session, err = %v", err)
	}

	ticket, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, sessionID, controlInstallation)
	if err != nil {
		t.Fatalf("mint for a live session this process lost: %v", err)
	}
	rebuilt, err := f.manager.GetSession(sessionID)
	if err != nil {
		t.Fatalf("the mint did not rebuild the session: %v", err)
	}
	if rebuilt.UserID != f.authority.identity.UserID || rebuilt.ProfileID != f.authority.identity.ProfileID || rebuilt.PlayMethod != playback.PlayDirect {
		t.Fatalf("rebuilt session = user %d profile %q method %q", rebuilt.UserID, rebuilt.ProfileID, rebuilt.PlayMethod)
	}

	// The lane opens on the rebuilt session like on one the media path rebuilt.
	f.session = rebuilt
	conn, resp, err := f.dial(t, ticket, nil) //nolint:bodyclose // dial registers response body cleanup.
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("control lane did not open: err %v", err)
	}
	f.hello(t, conn)
}

// The session the ticket rebuilt is the one the player's next media request
// finds: the stream keeps serving the same file to the same owner.
func TestBloemControlMintRebuildKeepsTheStreamServing(t *testing.T) {
	f, sessionID := newControlRebuildFixture(t)
	card := f.apiDirectCard(sessionID)
	streamURL := planStreamURL(t, card, controlRebuildSecret)
	f.saveLiveAttempt(t, sessionID, "plan:current", streamURL)
	if _, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, sessionID, controlInstallation); err != nil {
		t.Fatalf("mint: %v", err)
	}

	streams := NewStreamHandler(f.manager, testPlaybackFileResolver{file: &models.MediaFile{ID: card.MediaFileID, FilePath: writePlaybackTestMediaFile(t, "movie.mp4")}})
	streams.JWTSecret = controlRebuildSecret
	identity := f.authority.identity
	ctx := apimw.SetClaims(context.Background(), &auth.Claims{UserID: identity.UserID, Role: "user", TokenType: auth.TokenTypeAccess})
	ctx = access.SetScope(ctx, access.Scope{UserID: identity.UserID})
	ctx = apimw.SetProfileID(ctx, identity.ProfileID)
	route := chi.NewRouteContext()
	route.URLParams.Add("session_id", sessionID)
	request := httptest.NewRequestWithContext(context.WithValue(ctx, chi.RouteCtxKey, route), http.MethodGet, "/api/v2"+streamURL, nil)
	recorder := httptest.NewRecorder()
	streams.HandleStream(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "video" {
		t.Fatalf("stream after the control rebuild = %d %q, want the file", recorder.Code, recorder.Body.String())
	}
}

// A header-authenticated attempt carries no token in its plan; its card is the
// API's stored recovery card, which the media path reads for the same rebuild.
func TestBloemControlMintRebuildsAHeaderAuthenticatedSessionFromItsStoredCard(t *testing.T) {
	f, sessionID := newControlRebuildFixture(t)
	card := f.apiDirectCard(sessionID)
	card.PlanID = "plan:current"
	card.RequireMediaAuthorization = true
	f.pb.SessionRecipeStore = &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{sessionID: card}}
	f.saveLiveAttempt(t, sessionID, "plan:current", "/stream/"+sessionID, playback.FeatureHeaderAuthenticatedMediaV3)

	if _, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, sessionID, controlInstallation); err != nil {
		t.Fatalf("mint for a header-authenticated session this process lost: %v", err)
	}
	rebuilt, err := f.manager.GetSession(sessionID)
	if err != nil {
		t.Fatalf("the mint did not rebuild the session: %v", err)
	}
	if !rebuilt.RequireMediaAuthorization {
		t.Fatal("a header-authenticated session must keep requiring media authorization")
	}
}

// Everything the media path would refuse, or rebuilds only together with its
// runtime, is left to the media path: the mint answers as Silo does and this
// process still holds nothing.
func TestBloemControlMintLeavesUnsafeRebuildsToTheMediaPath(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(t *testing.T, f *bloemControlSocketFixture, sessionID string)
		want  error
	}{
		"plan carries no token": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.saveLiveAttempt(t, sessionID, "plan:current", "/stream/"+sessionID)
		}, ErrPlaybackControlSocketSessionResuming},
		"token signed by another server": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(sessionID), "another-secret"))
		}, ErrPlaybackControlSocketSessionResuming},
		"token names another session": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(uuid.NewString()), controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"token belongs to another account": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			card := f.apiDirectCard(sessionID)
			card.UserID++
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, card, controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"proxy-served route": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			card := f.apiDirectCard(sessionID)
			card.RoutingEgress = string(noderouting.EgressProxy)
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, card, controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"tone-mapped transcode rebuilds with its runtime": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			card := f.apiDirectCard(sessionID)
			card.ToneMapMode = tonemap.ModeHardware
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, card, controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"keyframe-planned transcode rebuilds with its runtime": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			card := f.apiDirectCard(sessionID)
			card.KeyframePlaylist = true
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, card, controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"header-authenticated attempt without a stored card": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(sessionID), controlRebuildSecret), playback.FeatureHeaderAuthenticatedMediaV3)
		}, ErrPlaybackControlSocketSessionResuming},
		"terminated session stays revoked": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.pb.StreamDeny = playback.NewStreamDeny(redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}))
			f.pb.StreamDeny.Deny(t.Context(), sessionID)
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(sessionID), controlRebuildSecret))
		}, ErrPlaybackControlSocketSessionResuming},
		"stopped attempt has ended": {func(t *testing.T, f *bloemControlSocketFixture, sessionID string) {
			f.saveLiveAttempt(t, sessionID, "plan:current", planStreamURL(t, f.apiDirectCard(sessionID), controlRebuildSecret))
			if _, _, err := f.pb.PlanStoreV3.(*playback.MemoryPlanStoreV3).StopAttempt(t.Context(), sessionID, uuid.NewString(), nil); err != nil {
				t.Fatal(err)
			}
		}, ErrPlaybackControlSocketSessionEnded},
	} {
		t.Run(name, func(t *testing.T) {
			f, sessionID := newControlRebuildFixture(t)
			tc.setup(t, f, sessionID)
			if _, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, sessionID, controlInstallation); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, err := f.manager.GetSession(sessionID); !errors.Is(err, playback.ErrSessionNotFound) {
				t.Fatalf("the mint rebuilt a session it must leave to the media path (err = %v)", err)
			}
		})
	}
}
