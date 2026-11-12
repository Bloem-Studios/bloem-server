package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

const compatTestCredential = "compat-session-token"

type compatSessionsFixture struct {
	session   NativeVideoCompatSession
	err       error
	liveErr   error
	rechecked []string
	digests   []string
}

func (c *compatSessionsFixture) Recheck(_ context.Context, credential string) (NativeVideoCompatSession, error) {
	c.rechecked = append(c.rechecked, credential)
	if credential != compatTestCredential {
		return NativeVideoCompatSession{}, errors.New("unknown compat session")
	}
	return c.session, c.err
}

func (c *compatSessionsFixture) Live(_ context.Context, userID int, profileID, digest string) error {
	c.digests = append(c.digests, digest)
	if userID != c.session.UserID || profileID != c.session.ProfileID {
		return errors.New("compat session belongs to someone else")
	}
	return c.liveErr
}

type compatHostStub struct{}

func (compatHostStub) Handles(string) bool                                          { return true }
func (compatHostStub) ServeDirect(http.ResponseWriter, *http.Request, string) error { return nil }
func (compatHostStub) Input(context.Context, string) (playback.TranscodeInputBinding, error) {
	return playback.TranscodeInputBinding{}, errors.New("unused")
}

// compatPolicy turns a native policy fixture into the Jellyfin listener's:
// the compat session is the credential, already PIN-verified at sign-in.
func compatPolicy(f *nativeVideoPolicyFixture) *compatSessionsFixture {
	sessions := &compatSessionsFixture{session: NativeVideoCompatSession{UserID: 7, ProfileID: "profile", LoginSessionID: "login-session", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}}
	compat := NewNativeVideoCompat()
	compat.SetSessions(sessions)
	f.policy.compat = compat
	f.viewer.scope.ProfileVerified = true
	return sessions
}

func compatStreamContext(stream playback.ContextStream) context.Context {
	ctx := playback.WithContextMediaHost(context.Background(), compatHostStub{})
	playback.NoteContextStream(ctx, stream)
	return ctx
}

func compatTestStream() playback.ContextStream {
	return playback.ContextStream{Credential: compatTestCredential, UserID: 7, ProfileID: "profile", PlaybackSessionID: "play-session", MediaFileID: 11}
}

func TestNativeVideoCompatStreamAuthorizesFromItsCompatSession(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	sessions := compatPolicy(f)
	ctx := compatStreamContext(compatTestStream())
	authority, err := nativeVideoAuthority(ctx, f.file.ID, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.AuthorizeSource(ctx, f.file, f.source); err != nil {
		t.Fatal(err)
	}
	if f.viewer.last.UserID != 7 || f.viewer.last.ProfileID != "profile" || !f.viewer.last.SkipPINVerification || f.viewer.last.SessionID != "login-session" {
		t.Fatalf("viewer input = %+v; want the compat subject with its login session", f.viewer.last)
	}
	if len(sessions.rechecked) < 2 {
		t.Fatalf("compat session rechecked %d times; want at capture and on every authority check", len(sessions.rechecked))
	}
}

func TestNativeVideoCompatStreamRefusals(t *testing.T) {
	for _, kind := range []string{"no-compat", "session-gone", "session-other-profile", "api-key-session", "login-changed", "playback-session-other-profile", "playback-session-other-file", "playback-session-missing", "denied", "other-file", "tenant", "viewer", "catalog", "source", "no-stream"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeVideoPolicyFixture(t)
			sessions := compatPolicy(f)
			stream := compatTestStream()
			fileID := f.file.ID
			switch kind {
			case "no-compat":
				f.policy.compat = nil
			case "session-gone":
				sessions.err = errors.New("signed out")
			case "session-other-profile":
				sessions.session.ProfileID = "other"
			case "api-key-session":
				// An sa_ admin key synthesizes a compat session with no login
				// session; native files refuse it in v1.
				sessions.session.LoginSessionID = ""
			case "login-changed":
				authority, err := nativeVideoAuthority(compatStreamContext(stream), fileID, f.policy)
				if err != nil {
					t.Fatal(err)
				}
				sessions.session.LoginSessionID = "another-login"
				if err := authority.AuthorizeSource(t.Context(), f.file, f.source); err == nil {
					t.Fatal("authority survived a changed login session")
				}
				return
			case "playback-session-other-profile":
				f.session.ProfileID = "other"
			case "playback-session-other-file":
				f.session.MediaFileID = 12
			case "playback-session-missing":
				f.session = nil
			case "denied":
				f.denied = true
			case "other-file":
				fileID = 12
			case "tenant":
				f.tenant.err = errors.New("membership ended")
			case "viewer":
				f.viewer.scope.PlaybackAllowed = false
			case "catalog":
				f.fileErr = errors.New("library access removed")
			case "source":
				f.sourceErr = errors.New("source disabled")
			case "no-stream":
				stream.PlaybackSessionID = ""
			}
			ctx := compatStreamContext(stream)
			authority, err := nativeVideoAuthority(ctx, fileID, f.policy)
			if err == nil {
				err = authority.AuthorizeSource(ctx, f.file, f.source)
			}
			if err == nil {
				t.Fatal("compat stream authorized")
			}
		})
	}
}

func compatDelegationFixture(t *testing.T) (*nativeVideoDelegationFixture, *compatSessionsFixture, handlers.NativeVideoBinding) {
	t.Helper()
	d := newNativeVideoDelegationFixture(t)
	sessions := compatPolicy(d.base)
	authority, err := nativeVideoDelegation(compatStreamContext(compatTestStream()), d.base.policy)
	if err != nil {
		t.Fatal(err)
	}
	binding := handlers.NativeVideoBinding{SessionID: "play-session", UserID: 7, ProfileID: "profile", Authority: authority, Identity: nativestorage.PlaybackIdentity{FileID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: d.base.file.FilePath}}
	d.base.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return binding, nil }
	return d, sessions, binding
}

func TestNativeVideoCompatDelegationCarriesNoCredential(t *testing.T) {
	d, sessions, binding := compatDelegationFixture(t)
	a := binding.Authority
	if a.TokenType != nativeVideoCompatTokenType || a.AuthMethod != nativeVideoCompatTokenType || a.LoginSessionID != "login-session" || a.PlaybackSessionID != "play-session" || !a.PINVerificationSkipped || a.ProfilePINRevision != 1 || a.ProfileCredentialRevision != 2 || a.Tenant != d.base.tenant.tenant || a.CompatSessionDigest == "" {
		t.Fatalf("compat delegation = %+v", a)
	}
	if !a.ExpiresAt.After(time.Now()) || a.ExpiresAt.After(time.Now().Add(playback.MaxTokenTTL+time.Minute)) || a.ExpiresAt.After(d.login.ExpiresAt) {
		t.Fatalf("compat delegation expiry %v outside its login and token bounds", a.ExpiresAt)
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(compatTestCredential)) {
		t.Fatal("binding contains the compat session token")
	}
	authority, err := nativeVideoDelegatedAuthority(t.Context(), d.base.file, binding, d.base.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.AuthorizeSource(t.Context(), d.base.file, d.base.source); err != nil {
		t.Fatal(err)
	}
	if len(sessions.digests) == 0 || sessions.digests[len(sessions.digests)-1] != a.CompatSessionDigest {
		t.Fatal("delegated authority did not recheck the compat session row")
	}
}

func TestNativeVideoCompatDelegatedAuthorityRejectsCurrentRevocations(t *testing.T) {
	for _, kind := range []string{"compat-row-gone", "no-compat", "login-revoked", "login-expired", "login-inactive", "login-other-profile", "account", "pin", "credential", "profile-removed", "profile-tenant", "tenant", "viewer", "library", "rating", "source", "session-other-profile", "denied", "expiry", "other-playback-session"} {
		t.Run(kind, func(t *testing.T) {
			d, sessions, binding := compatDelegationFixture(t)
			f := d.base
			switch kind {
			case "compat-row-gone":
				sessions.liveErr = errors.New("compat session deleted")
			case "no-compat":
				f.policy.compat = nil
			case "login-revoked":
				now := time.Now()
				d.login.RevokedAt = &now
			case "login-expired":
				d.login.ExpiresAt = time.Now().Add(-time.Minute)
			case "login-inactive":
				f.login.active = false
			case "login-other-profile":
				other := "other"
				d.login.ProfileID = &other
			case "account":
				f.login.enabled = false
			case "pin":
				d.profile.PINRevision++
			case "credential":
				d.profile.CredentialRevision++
			case "profile-removed":
				f.policy.loadProfile = func(context.Context, int, string) (*userstore.Profile, error) {
					return nil, errors.New("profile removed")
				}
			case "profile-tenant":
				d.profile.OrganizationID = uuid.New().String()
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "viewer":
				f.viewer.scope.PlaybackAllowed = false
			case "library", "rating":
				f.fileErr = errors.New("hidden by " + kind)
			case "source":
				f.sourceErr = errors.New("source disabled")
			case "session-other-profile":
				f.session.ProfileID = "other"
			case "denied":
				f.denied = true
			case "expiry":
				binding.Authority.ExpiresAt = time.Now().Add(-time.Minute)
			case "other-playback-session":
				binding.Authority.PlaybackSessionID = "other-session"
			}
			authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
			if err == nil {
				err = authority.AuthorizeSource(t.Context(), f.file, f.source)
			}
			if err == nil {
				t.Fatal("delegated compat authority survived a revocation")
			}
		})
	}
}

// The native API never takes the compat path: without a noted compat stream
// a request still needs native credentials.
func TestNativeVideoCompatPathNeedsANotedStream(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	compatPolicy(f)
	ctx := playback.WithContextMediaHost(context.Background(), compatHostStub{})
	if _, err := nativeVideoAuthority(ctx, f.file.ID, f.policy); err == nil {
		t.Fatal("a request without a compat stream or native credential was authorized")
	}
}
