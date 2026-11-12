package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// downloadContext is a download request after authentication and viewer
// access: claims, viewer scope, profile and tenant, and no playback session.
func (d *nativeVideoDelegationFixture) downloadContext(claims *auth.Claims) context.Context {
	f := d.base
	ctx := access.SetScope(context.Background(), f.viewer.scope)
	ctx = middleware.SetProfileID(ctx, "profile")
	ctx = middleware.SetClaims(ctx, claims)
	return tenancy.WithContext(ctx, f.tenant.tenant)
}

func TestNativeVideoDownloadAuthorityFollowsTheLoginSession(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	ctx := d.downloadContext(f.claims)
	delegation, err := nativeVideoDownloadDelegation(ctx, 11, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if delegation.LoginSessionID != "login-session" || delegation.PlaybackSessionID != "" || delegation.ProfilePINRevision != 1 || delegation.ProfileCredentialRevision != 2 || delegation.Tenant != f.tenant.tenant {
		t.Fatalf("delegation=%+v", delegation)
	}
	if !delegation.ExpiresAt.After(f.claims.ExpiresAt.Time) {
		t.Fatal("a download must outlive its access token, bound to the login session")
	}
	authority, err := nativeVideoDownloadAuthority(ctx, 11, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.AuthorizeSource(context.Background(), f.file, f.source); err != nil {
		t.Fatalf("authorized download refused: %v", err)
	}
	other, err := nativeVideoDownloadAuthority(ctx, 12, f.policy)
	if err == nil {
		err = other.AuthorizeSource(context.Background(), f.file, f.source)
	}
	if err == nil {
		t.Fatal("authority for one file opened another")
	}
}

func TestNativeVideoDownloadAuthorityStopsOnRevocation(t *testing.T) {
	for _, kind := range []string{"account", "login-revoked", "login-inactive", "pin", "credential", "tenant", "viewer", "catalog", "source"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			authority, err := nativeVideoDownloadAuthority(d.downloadContext(f.claims), 11, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "account":
				f.login.enabled = false
			case "login-revoked":
				now := time.Now()
				d.login.RevokedAt = &now
			case "login-inactive":
				f.login.active = false
			case "pin":
				d.profile.PINRevision++
			case "credential":
				d.profile.CredentialRevision++
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "viewer":
				f.viewer.scope.PlaybackAllowed = false
			case "catalog":
				f.fileErr = errors.New("hidden")
			case "source":
				f.sourceErr = errors.New("source hidden")
			}
			if err := authority.AuthorizeSource(context.Background(), f.file, f.source); err == nil {
				t.Fatal("download survived a revocation")
			}
		})
	}
}

func TestNativeVideoDownloadAuthorityCredentials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*nativeVideoDelegationFixture) *auth.Claims
		ok    bool
	}{
		{"link for this file", func(d *nativeVideoDelegationFixture) *auth.Claims {
			return &auth.Claims{UserID: 7, ProfileID: "profile", SessionID: "login-session", TokenType: auth.TokenTypeDirectDownloadLink, FileID: 11}
		}, true},
		{"link for another file", func(d *nativeVideoDelegationFixture) *auth.Claims {
			return &auth.Claims{UserID: 7, ProfileID: "profile", SessionID: "login-session", TokenType: auth.TokenTypeDirectDownloadLink, FileID: 12}
		}, false},
		{"unscoped API key", func(d *nativeVideoDelegationFixture) *auth.Claims {
			return &auth.Claims{UserID: 7, TokenType: auth.TokenTypeAPIKey, APIKeyID: 99}
		}, true},
		{"scoped API key", func(d *nativeVideoDelegationFixture) *auth.Claims {
			d.key.Scopes = []string{auth.ScopeLibrariesRead}
			return &auth.Claims{UserID: 7, TokenType: auth.TokenTypeAPIKey, APIKeyID: 99}
		}, false},
		{"other account", func(d *nativeVideoDelegationFixture) *auth.Claims {
			return &auth.Claims{UserID: 8, SessionID: "login-session", TokenType: auth.TokenTypeAccess}
		}, false},
		{"refresh token", func(d *nativeVideoDelegationFixture) *auth.Claims {
			return &auth.Claims{UserID: 7, SessionID: "login-session", TokenType: auth.TokenTypeRefresh}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			claims := tc.setup(d)
			authority, err := nativeVideoDownloadAuthority(d.downloadContext(claims), 11, d.base.policy)
			if err == nil {
				err = authority.AuthorizeSource(context.Background(), d.base.file, d.base.source)
			}
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestNativeVideoDownloadAuthorityRefusesMissingTenant(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	ctx := access.SetScope(context.Background(), f.viewer.scope)
	ctx = middleware.SetClaims(ctx, f.claims)
	if _, err := nativeVideoDownloadAuthority(ctx, 11, f.policy); err == nil {
		t.Fatal("authority without a tenant")
	}
	other := f.tenant.tenant
	other.OrganizationID = uuid.New()
	if _, err := nativeVideoDownloadAuthority(tenancy.WithContext(ctx, other), 11, f.policy); err == nil {
		t.Fatal("authority for another organization")
	}
}

func TestNativeVideoDownloadAuthorityFromAJellyfinSession(t *testing.T) {
	for _, kind := range []string{"allowed", "admin-key", "other-profile", "signed-out", "session-deleted"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			sessions := compatPolicy(f)
			credential := compatTestCredential
			profile := "profile"
			switch kind {
			case "admin-key":
				credential = "sa_admin"
				sessions.session.LoginSessionID = ""
			case "other-profile":
				profile = "other"
			}
			// The Jellyfin listener carries no Bloem claims or viewer scope.
			ctx := tenancy.WithContext(WithNativeVideoCompatDownload(context.Background(), credential, 7, profile), f.tenant.tenant)
			authority, err := nativeVideoDownloadAuthority(ctx, 11, f.policy)
			if err == nil {
				switch kind {
				case "signed-out":
					now := time.Now()
					d.login.RevokedAt = &now
				case "session-deleted":
					sessions.liveErr = errors.New("gone")
				}
				err = authority.AuthorizeSource(context.Background(), f.file, f.source)
			}
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("err=%v", err)
			}
			if kind == "allowed" && len(sessions.digests) == 0 {
				t.Fatal("compat session not rechecked")
			}
		})
	}
}
