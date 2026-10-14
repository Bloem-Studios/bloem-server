package preview_test

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

func TestPreviewAdminEnsureIsAtomicAndIdempotent(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	admin := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := admin.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	again, err := admin.EnsureAccount(ctx, actor, "operator-test")
	if err != nil || again != id {
		t.Fatalf("repeat ensure: %d %v", again, err)
	}
	var password, primary, previewOnly bool
	if err := pool.QueryRow(ctx, `SELECT local_password_login_enabled FROM users WHERE id=$1`, id).Scan(&password); err != nil || password {
		t.Fatalf("password enabled: %v %v", password, err)
	}
	if err := pool.QueryRow(ctx, `SELECT is_primary FROM user_profiles WHERE user_id=$1`, id).Scan(&primary); err != nil || !primary {
		t.Fatalf("primary profile: %v %v", primary, err)
	}
	previewOnly, err = auth.NewSessionRepository(pool).AccountPreviewOnly(ctx, id)
	if err != nil || !previewOnly {
		t.Fatalf("missing preview template: %v %v", previewOnly, err)
	}
	if _, err := admin.EnsureAccount(ctx, actor, "second-operator"); err == nil {
		t.Fatal("another operator took over")
	}
	if _, err := admin.EnsureAccount(ctx, id, "operator-test"); err == nil {
		t.Fatal("preview account administered itself")
	}
}

func TestPreviewAdminConcurrentIssuersCannotTakeOver(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	start := make(chan struct{})
	result := make(chan error, 2)
	var wg sync.WaitGroup
	for _, issuer := range []string{"first-operator", "second-operator"} {
		wg.Go(func() { <-start; _, err := a.EnsureAccount(ctx, actor, issuer); result <- err })
	}
	close(start)
	wg.Wait()
	close(result)
	success, conflict := 0, 0
	for err := range result {
		if err == nil {
			success++
		} else if errors.Is(err, preview.ErrInvalidSettings) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent ownership: %d success, %d conflict", success, conflict)
	}
	var accounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username='bloem-preview'`).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatalf("duplicate shared accounts: %d %v", accounts, err)
	}
}

func TestPreviewAdminEmptyLibrarySelectionGrantsNothing(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := a.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	if _, err = a.SetLibraries(ctx, actor, "operator-test", []int{}); err != nil {
		t.Fatal(err)
	}
	var libs []int
	if err = pool.QueryRow(ctx, `SELECT g.library_ids FROM organization_memberships m JOIN access_groups g ON g.id=m.access_group_id WHERE m.account_id=$1`, id).Scan(&libs); err != nil {
		t.Fatal(err)
	}
	if libs == nil || len(libs) != 0 {
		t.Fatalf("empty selection grants libraries: %v (nil=%v)", libs, libs == nil)
	}
}

func TestPreviewAdminReservedNameCollisionCannotBeAdopted(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	if _, err := pool.Exec(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES('bloem-preview','collision@example.test','','user')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE username='bloem-preview'`) })
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	if _, err := a.EnsureAccount(ctx, actor, "operator-test"); !errors.Is(err, preview.ErrInvalidSettings) {
		t.Fatalf("collision adopted: %v", err)
	}
	s, err := a.Settings(ctx, actor)
	if err != nil || s.AccountID != 0 || s.Issuer != "" {
		t.Fatalf("partial setup persisted: %+v %v", s, err)
	}
}

func TestPreviewAdminProbeDoesNotConsumeAndCapacityIgnoresIdleGuests(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := a.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	pub, priv := newKey(t)
	s := validSettings(t, id, pub)
	s.Issuer = "operator-test"
	if err = a.Configure(ctx, actor, s, "lib.example.test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	jti := uuid.NewString()
	raw := sign(t, priv, jwt.MapClaims{"iss": "operator-test", "aud": "server-test", "host": s.Host, "jti": jti, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()})
	for range 2 {
		if _, err = a.Verify(ctx, actor, raw); err != nil {
			t.Fatal(err)
		}
	}
	var uses int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM bloem_preview_ticket_uses WHERE jti=$1`, jti).Scan(&uses); err != nil || uses != 0 {
		t.Fatalf("probe consumed ticket: %d %v", uses, err)
	}
	var sessionIDs []string
	for range 2 {
		guests := auth.NewPreviewGuests(pool, auth.NewJWTService("test", time.Hour, time.Hour), nil)
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		tokens, e := guests.CreateInTx(ctx, tx, auth.NewPreviewGuest{AccountID: id, ExpiresAt: now.Add(time.Hour), IdleTTL: 20 * time.Minute, ClientBucket: "bucket"})
		if e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		sessionIDs = append(sessionIDs, tokens.SessionID)
	}
	if _, err = pool.Exec(ctx, `UPDATE auth_sessions SET created_at=now()-interval '1 hour',last_seen_at=now()-interval '21 minutes' WHERE id=$1`, sessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	cap, err := a.Capacity(ctx, actor)
	if err != nil || !cap.AccountReady || cap.Active != 1 {
		t.Fatalf("capacity counts idle: %+v %v", cap, err)
	}
	if err = a.Revoke(ctx, actor, "operator-test"); err != nil {
		t.Fatal(err)
	}
	cap, err = a.Capacity(ctx, actor)
	if err != nil || cap.Active != 0 {
		t.Fatalf("revoke left guests: %+v %v", cap, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM user_profiles WHERE user_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Verify(ctx, actor, raw); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatalf("probe reported readiness without a primary profile: %v", err)
	}
}

func TestPreviewAdminConfigurationRefusesServedHosts(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := a.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	pub, _ := newKey(t)
	s := validSettings(t, id, pub)
	s.Issuer = "operator-test"
	if _, err := pool.Exec(ctx, `INSERT INTO server_settings(key,value) VALUES('server.public_url','https://lib.example.test') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"lib.example.test", "admin.example.test"} {
		s.Host = host
		if err := a.Configure(ctx, actor, s, "admin.example.test:443"); err == nil {
			t.Fatalf("accepted served host %s", host)
		}
	}
	s.Host = "browse.example.test"
	if err := a.Configure(ctx, actor, s, "admin.example.test"); err != nil {
		t.Fatal(err)
	}
	other, _ := newKey(t)
	if err := a.RotateKeys(ctx, actor, s.Issuer, []ed25519.PublicKey{pub, other}); err != nil {
		t.Fatal(err)
	}
	got, err := preview.NewStore(pool).Get(ctx)
	if err != nil || len(got.TicketPublicKeys) != 2 {
		t.Fatalf("overlap rotation failed: %v", err)
	}
	cap, err := a.Capacity(ctx, actor)
	if err != nil || cap.Active != 0 || cap.Maximum != 200 {
		t.Fatalf("capacity: %+v %v", cap, err)
	}
	if err := a.Revoke(ctx, actor, s.Issuer); err != nil {
		t.Fatal(err)
	}
}
