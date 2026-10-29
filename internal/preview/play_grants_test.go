package preview_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func (f *exchangeFixture) playTicket(t *testing.T, grant string, play time.Duration, grantEnds time.Time) string {
	now := time.Now()
	return sign(t, f.priv, jwt.MapClaims{"iss": want.Issuer, "aud": want.Audience, "host": f.settings.Host,
		"jti": uuid.NewString()[:8] + uuid.NewString()[:8] + uuid.NewString()[:8], "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		"item": "movie:7", "play": int64(play / time.Second), "grant": grant, "grant_exp": grantEnds.Unix()})
}

func (f *exchangeFixture) setFreePlay(t *testing.T, on bool) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_settings SET free_play_enabled=$1`, on); err != nil {
		t.Fatal(err)
	}
}

func (f *exchangeFixture) sessionGrant(t *testing.T, session string) string {
	t.Helper()
	var grant *string
	if err := f.pool.QueryRow(f.ctx, `SELECT play_grant_id FROM bloem_preview_guest_sessions WHERE session_id=$1`, session).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if grant == nil {
		return ""
	}
	return *grant
}

// With free play off, an allowance in the ticket is ignored: the visitor
// browses as any guest and no grant is recorded.
func TestExchangeIgnoresAPlayAllowanceWhileFreePlayIsOff(t *testing.T) {
	f := newExchangeFixture(t, 200)
	grant := "OffOffOffOffOffOffOffOf"
	res, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, time.Now().Add(24*time.Hour)), Host: f.settings.Host, ClientIP: "198.51.100.20"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.sessionGrant(t, res.Tokens.SessionID); got != "" || res.FreePlay {
		t.Fatalf("session grant = %q, free play = %v", got, res.FreePlay)
	}
	var rows int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM bloem_preview_play_grants WHERE grant_id=$1`, grant).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("grant rows = %d, %v", rows, err)
	}
}

// The first exchange fixes a grant's allowance and end; a later ticket for the
// same grant, even one signed with more time, neither raises them nor resets
// what was used.
func TestExchangeBindsTheSessionToAGrantThatNeverGrows(t *testing.T) {
	f := newExchangeFixture(t, 200)
	f.setFreePlay(t, true)
	grant := "GrantGrantGrantGrantGr"
	ends := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	res, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, ends), Host: f.settings.Host, ClientIP: "198.51.100.21"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.sessionGrant(t, res.Tokens.SessionID); got != grant || !res.FreePlay {
		t.Fatalf("session grant = %q, free play = %v", got, res.FreePlay)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_play_grants SET used_seconds=600 WHERE grant_id=$1`, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 4*time.Hour, ends.Add(48*time.Hour)), Host: f.settings.Host, ClientIP: "198.51.100.22"}); err != nil {
		t.Fatal(err)
	}
	var allowance, used int
	var expires time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT allowance_seconds, used_seconds, expires_at FROM bloem_preview_play_grants WHERE grant_id=$1`, grant).Scan(&allowance, &used, &expires); err != nil {
		t.Fatal(err)
	}
	if allowance != 1800 || used != 600 || !expires.Equal(ends) {
		t.Fatalf("grant = %d/%d until %s, want 1800/600 until %s", used, allowance, expires, ends)
	}
}

// A grant ticket opened in a tab that still holds a browse-only guest session
// gets a new session bound to the grant; reusing the old one would leave the
// visitor locked out of what they were just given. A session already bound to
// that grant is reused as before.
func TestExchangeReusesASessionOnlyForTheSameGrant(t *testing.T) {
	f := newExchangeFixture(t, 200)
	f.setFreePlay(t, true)
	browse, err := f.exchange(t, "198.51.100.23", "")
	if err != nil {
		t.Fatal(err)
	}
	grant := "ReuseReuseReuseReuseRe"
	ends := time.Now().Add(24 * time.Hour)
	bound, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, ends), RefreshToken: browse.Tokens.RefreshToken, Host: f.settings.Host, ClientIP: "198.51.100.23"})
	if err != nil {
		t.Fatal(err)
	}
	if bound.Reused || !bound.FreePlay || bound.Tokens.SessionID == browse.Tokens.SessionID || f.sessionGrant(t, bound.Tokens.SessionID) != grant {
		t.Fatalf("grant ticket over a browse-only session: reused=%v session=%s grant=%q", bound.Reused, bound.Tokens.SessionID, f.sessionGrant(t, bound.Tokens.SessionID))
	}
	again, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, ends), RefreshToken: bound.Tokens.RefreshToken, Host: f.settings.Host, ClientIP: "198.51.100.23"})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Reused || again.Tokens.SessionID != bound.Tokens.SessionID || !again.FreePlay {
		t.Fatalf("same grant again: reused=%v session=%s", again.Reused, again.Tokens.SessionID)
	}
	// The ordinary launch link in a free-play tab keeps the free-play session.
	plain, err := f.exchange(t, "198.51.100.23", again.Tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if !plain.Reused || plain.Tokens.SessionID != bound.Tokens.SessionID || !plain.FreePlay {
		t.Fatalf("plain ticket in a free-play tab: reused=%v session=%s free play=%v", plain.Reused, plain.Tokens.SessionID, plain.FreePlay)
	}
}

// A grant is live only while the preview and free play are on, something is
// left of it and its end has not passed; LiveFreePlay is what guest admission
// asks on every request.
func TestLiveFreePlayFollowsTheGrantAndTheSwitches(t *testing.T) {
	f := newExchangeFixture(t, 200)
	f.setFreePlay(t, true)
	grant := "LiveLiveLiveLiveLiveLi"
	res, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, time.Now().Add(24*time.Hour)), Host: f.settings.Host, ClientIP: "198.51.100.30"})
	if err != nil {
		t.Fatal(err)
	}
	browse, err := f.exchange(t, "198.51.100.31", "")
	if err != nil {
		t.Fatal(err)
	}
	reader := preview.NewReader(f.store, 0)
	live := func() bool {
		t.Helper()
		got, ok, err := reader.LiveFreePlay(f.ctx, res.Tokens.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && got != grant {
			t.Fatalf("grant = %q", got)
		}
		return ok
	}
	if !live() {
		t.Fatal("fresh grant is not live")
	}
	if _, ok, _ := reader.LiveFreePlay(f.ctx, browse.Tokens.SessionID); ok {
		t.Fatal("browse-only session reports free play")
	}
	set := func(sql string) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx, sql, grant); err != nil {
			t.Fatal(err)
		}
	}
	set(`UPDATE bloem_preview_play_grants SET used_seconds=allowance_seconds WHERE grant_id=$1`)
	if live() {
		t.Fatal("spent grant is live")
	}
	set(`UPDATE bloem_preview_play_grants SET used_seconds=0, expires_at=now()-interval '1 second' WHERE grant_id=$1`)
	if live() {
		t.Fatal("ended grant is live")
	}
	set(`UPDATE bloem_preview_play_grants SET expires_at=now()+interval '1 hour' WHERE grant_id=$1`)
	f.setFreePlay(t, false)
	if live() {
		t.Fatal("grant live with free play off")
	}
	f.setFreePlay(t, true)
	if !live() {
		t.Fatal("grant not live again with free play back on")
	}
}

// A free-play guest's presentation tells the app how much is left; once the
// grant is spent the app is told nothing more, and a browse-only guest never.
func TestPresentationCarriesTheGuestsFreePlay(t *testing.T) {
	f := newExchangeFixture(t, 200)
	f.setFreePlay(t, true)
	grant := "ShownShownShownShownSh"
	ends := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	res, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, 30*time.Minute, ends), Host: f.settings.Host, ClientIP: "198.51.100.50"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_play_grants SET used_seconds=120.6 WHERE grant_id=$1`, grant); err != nil {
		t.Fatal(err)
	}
	shown, err := f.store.Presentation(f.ctx, f.settings.AccountID, res.Tokens.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if shown.FreePlay == nil || shown.FreePlay.AllowanceSeconds != 1800 || shown.FreePlay.UsedSeconds != 120 || !shown.FreePlay.EndsAt.Equal(ends) {
		t.Fatalf("free play = %+v", shown.FreePlay)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_play_grants SET used_seconds=1800 WHERE grant_id=$1`, grant); err != nil {
		t.Fatal(err)
	}
	if shown, err = f.store.Presentation(f.ctx, f.settings.AccountID, res.Tokens.SessionID, true); err != nil || shown.FreePlay != nil {
		t.Fatalf("spent grant still shown: %+v %v", shown.FreePlay, err)
	}
	browse, err := f.exchange(t, "198.51.100.51", "")
	if err != nil {
		t.Fatal(err)
	}
	if shown, err = f.store.Presentation(f.ctx, f.settings.AccountID, browse.Tokens.SessionID, true); err != nil || shown.FreePlay != nil {
		t.Fatalf("browse-only guest shown free play: %+v %v", shown.FreePlay, err)
	}
}
