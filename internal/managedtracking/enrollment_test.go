package managedtracking

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"strconv"
	"strings"
	"testing"
)

func enrollmentFixture(t *testing.T) (*Service, int, Profile) {
	t.Helper()
	s, _, account, profile, install, _ := fixture(t)
	s.Cipher, _ = secret.New([]byte(strings.Repeat("fixture-only-key-", 3)))
	page, err := s.ListProfiles(t.Context(), install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Profiles {
		if p.AccountID == strconv.Itoa(account) && p.ProfileID == profile {
			return s, install, p
		}
	}
	t.Fatal("profile absent")
	return nil, 0, Profile{}
}
func TestEnrollmentAtomicEncryptedReplayAndRevocation(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	ctx := t.Context()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture-only", Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal("replay", err)
	}
	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	account, _ := strconv.Atoi(p.AccountID)
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	conn, found, err := repo.GetConnection(ctx, key, account, p.ProfileID)
	if err != nil || !found {
		t.Fatalf("connection: %v %v", found, err)
	}
	if conn.AccessToken != e.Credential || conn.SecretAttributes["profile_id"] != p.ProfileID || conn.ImportWatchedEnabled || conn.ImportProgressEnabled || !conn.ScrobbleEnabled {
		t.Fatalf("wrong managed settings")
	}
	var stored string
	if err = s.Pool.QueryRow(ctx, `SELECT plugin_credentials FROM watch_provider_connections WHERE id=$1`, conn.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !secret.IsEncrypted(stored) || strings.Contains(stored, e.Credential) {
		t.Fatal("credential not sealed")
	}
	bad := e
	bad.Credential = "ptbs_conflicting"
	if !errors.Is(s.EnsureConnection(ctx, id, bad), ErrConflict) {
		t.Fatal("conflicting replay accepted")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE user_profiles SET name='Renamed' WHERE id=$1`, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.EnsureConnection(ctx, id, e), ErrAuthority) {
		t.Fatal("stale source accepted")
	}
	if err = s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	conn, found, err = repo.GetConnection(ctx, key, account, p.ProfileID)
	if err != nil || !found || conn.ScrobbleEnabled || conn.AccessToken != "" {
		t.Fatal("revocation retained usable connection")
	}
	if _, err = s.GetInfo(ctx, id); !errors.Is(err, ErrAuthority) {
		t.Fatal("revoked authority survives")
	}
}
func TestEnrollmentFailsClosedWithoutCipherAndWrongProvider(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	info, _ := s.GetInfo(t.Context(), id)
	e := Enrollment{Profile: p, ProviderID: "other", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err := s.EnsureConnection(t.Context(), id, e); !errors.Is(err, ErrAuthority) {
		t.Fatal("wrong provider accepted")
	}
	e.ProviderID = "pastime"
	e.ID = EnrollmentID(info.InstanceID, e)
	s.Cipher = nil
	if err := s.EnsureConnection(context.Background(), id, e); !errors.Is(err, ErrAuthority) {
		t.Fatal("plaintext fallback accepted")
	}
}
func TestGenerationRenewsBeforeExpiryAndInvalidatesSnapshot(t *testing.T) {
	s, id, _ := enrollmentFixture(t)
	ctx := t.Context()
	before, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListProfiles(ctx, id, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE bloem_managed_grants SET renewed_at=clock_timestamp()-interval '31 days' WHERE installation_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ReadChanges(ctx, id)
	if err != nil || !pending {
		t.Fatal("renewal not pending")
	}
	after, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+1 || after.InstanceID != before.InstanceID {
		t.Fatal("durable renewal missing")
	}
	if _, err = s.ListProfiles(ctx, id, page.SnapshotID, page.NextCursor, 1); !errors.Is(err, ErrSnapshot) {
		t.Fatal("old generation snapshot accepted")
	}
}
func TestGrantReplayAndExplicitReactivationPreserveScope(t *testing.T) {
	s, id, _ := enrollmentFixture(t)
	ctx := t.Context()
	before, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Grant(ctx, id, before.Scope); err != nil {
		t.Fatal("grant replay rejected", err)
	}
	after, _ := s.GetInfo(ctx, id)
	if after != before {
		t.Fatal("replay changed authority")
	}
	if err = s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = s.Grant(ctx, id, before.Scope); err != nil {
		t.Fatal("explicit same-scope reactivation rejected", err)
	}
	after, _ = s.GetInfo(ctx, id)
	if after.InstanceID != before.InstanceID || after.Generation <= before.Generation {
		t.Fatal("reactivation reused credential epoch")
	}
}
