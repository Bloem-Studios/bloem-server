//go:build integration

package api

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A delegated compat authority holds only the digest of the compat session's
// token. The session is live while its row exists, unexpired, for the same
// account and profile.
func TestNativeVideoCompatSessionLiveByDigestDB(t *testing.T) {
	pool := coverRecipeDatabase(t)
	var uid, other int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('compat-viewer','compat@example.test','x','user',true) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('compat-other','compat-other@example.test','x','user',true) RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	insert := func(token string, user int, profile, expires string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `INSERT INTO jellycompat_sessions(token,username,account_username,profile_id,profile_name,pseudo_user_id,streamapp_user_id,streamapp_access_token,streamapp_refresh_token,streamapp_token_expiry,expires_at)
			VALUES($1,'viewer','viewer',$2,'Viewer',$3,$4,'sealed','sealed',now()+interval '1 hour',now()+$5::interval)`, token, profile, uuid.New(), user, expires); err != nil {
			t.Fatal(err)
		}
	}
	insert("live-token", uid, "profile", "1 hour")
	insert("expired-token", uid, "profile", "-1 minute")
	sessions := NewNativeVideoCompatSessions(pool, nil)
	if err := sessions.Live(t.Context(), uid, "profile", nativeVideoCompatDigest("live-token")); err != nil {
		t.Fatalf("live session refused: %v", err)
	}
	for name, check := range map[string]func() error{
		"wrong-token": func() error {
			return sessions.Live(t.Context(), uid, "profile", nativeVideoCompatDigest("other-token"))
		},
		"other-profile": func() error { return sessions.Live(t.Context(), uid, "other", nativeVideoCompatDigest("live-token")) },
		"other-account": func() error {
			return sessions.Live(t.Context(), other, "profile", nativeVideoCompatDigest("live-token"))
		},
		"expired": func() error {
			return sessions.Live(t.Context(), uid, "profile", nativeVideoCompatDigest("expired-token"))
		},
		"raw-token": func() error { return sessions.Live(t.Context(), uid, "profile", "live-token") },
	} {
		if err := check(); err == nil {
			t.Fatalf("%s: compat session reported live", name)
		}
	}
	// An organization removing the profile deletes its compat rows
	// (bloemProfileSessionRevoker); delivery then stops.
	if _, err := pool.Exec(t.Context(), `DELETE FROM jellycompat_sessions WHERE token='live-token'`); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Live(t.Context(), uid, "profile", nativeVideoCompatDigest("live-token")); err == nil {
		t.Fatal("deleted compat session reported live")
	}
	if _, err := NewNativeVideoCompatSessions(pool, nil).Recheck(t.Context(), "live-token"); !errors.Is(err, errNativeVideoCompatUnavailable) {
		t.Fatalf("recheck without a session store = %v", err)
	}
}
