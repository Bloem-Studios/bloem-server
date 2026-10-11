package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const testIssuer = "https://idp.example.test/application/o/silo/"

func authInstallation(t *testing.T, pool *pgxpool.Pool, plugin string, enabled bool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled) VALUES($1,'test','fixture',$2) RETURNING id`, plugin, enabled).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// tenantAccount creates an active member of tenant with one primary profile.
func tenantAccount(t *testing.T, pool *pgxpool.Pool, tenant string) (int, string) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	u, err := auth.NewUserRepository(pool).CreateInTransaction(ctx, tx, models.CreateUserInput{Username: "managed-identity-" + uuid.NewString(), Email: uuid.NewString() + "@example.test", Password: "synthetic-managed-test-only", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	var group int64
	if err = tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, tenant).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role,access_group_id) VALUES($1,$2,'active','user',$3) ON CONFLICT DO NOTHING`, tenant, u.ID, group); err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if err = pgstore.NewPostgresProvider(pool).CreateProfileInTransaction(ctx, tx, u.ID, userstore.Profile{ID: profile, Name: "Main", IsPrimary: true, OrganizationID: tenant, AccessGroupID: &group}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return u.ID, profile
}

// linkIdentity inserts an external sign-in identity with the column list
// Silo's linking uses.
func linkIdentity(t *testing.T, pool *pgxpool.Pool, install int64, account int, external, issuer string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_auth_identities(plugin_installation_id,external_subject,user_id,issuer,username,email,display_name,linked_at,last_sign_in_at,last_checked_at,last_check_status,last_authenticated_at)
 VALUES($1,$2,$3,$4,'person','person@example.test','Person',NOW(),NOW(),NOW(),'active',NOW()) RETURNING id`, install, external, account, issuer).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// projectedIdentity returns the identity and account revision every
// projection row of the account carries.
func projectedIdentity(t *testing.T, pool *pgxpool.Pool, account int) (string, string, uint64) {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT DISTINCT identity_issuer,identity_subject,account_revision FROM bloem_managed_profiles WHERE account_id=$1`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var issuer, subject string
	var revision uint64
	n := 0
	for rows.Next() {
		if err = rows.Scan(&issuer, &subject, &revision); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("account %d has %d distinct projections, want 1", account, n)
	}
	return issuer, subject, revision
}

func clockRevision(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var revision int64
	if err := pool.QueryRow(t.Context(), `SELECT revision FROM bloem_managed_clock`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func wantIdentity(t *testing.T, pool *pgxpool.Pool, account int, issuer, subject string) uint64 {
	t.Helper()
	gotIssuer, gotSubject, revision := projectedIdentity(t, pool, account)
	if gotIssuer != issuer || gotSubject != subject {
		t.Fatalf("account %d identity = (%q, %q), want (%q, %q)", account, gotIssuer, gotSubject, issuer, subject)
	}
	return revision
}

type identityLink struct {
	install          int64
	external, issuer string
}

func TestVerifiedIdentityDerivation(t *testing.T) {
	_, pool, _, _, _, tenant := fixture(t)
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	second := authInstallation(t, pool, "silo.auth.oidc", true)
	disabled := authInstallation(t, pool, "silo.auth.oidc", false)
	other := authInstallation(t, pool, "bloem.auth.other", true)
	for _, tc := range []struct {
		name  string
		links func(sub string) []identityLink
		want  bool
	}{
		{"empty issuer column takes the token issuer", func(s string) []identityLink { return []identityLink{{oidc, testIssuer + "|" + s, ""}} }, true},
		{"issuer column agrees", func(s string) []identityLink { return []identityLink{{oidc, testIssuer + "|" + s, testIssuer}} }, true},
		{"one identity in two installations", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s, ""}, {second, testIssuer + "|" + s, testIssuer}}
		}, true},
		{"disabled installation is ignored", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s, ""}, {disabled, testIssuer + "|" + uuid.NewString(), ""}}
		}, true},
		{"subject may contain the separator", func(s string) []identityLink { return []identityLink{{oidc, testIssuer + "|" + s + "|tail", ""}} }, true},
		{"only a disabled installation", func(s string) []identityLink { return []identityLink{{disabled, testIssuer + "|" + s, ""}} }, false},
		{"another auth plugin", func(s string) []identityLink { return []identityLink{{other, testIssuer + "|" + s, ""}} }, false},
		{"issuer column disagrees", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s, "https://other.example.test/"}}
		}, false},
		{"two subjects", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s, ""}, {second, testIssuer + "|" + uuid.NewString(), ""}}
		}, false},
		{"an entra key poisons a valid link", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s, ""}, {second, "tenant-" + s + "|object", ""}}
		}, false},
		{"plain http issuer", func(s string) []identityLink { return []identityLink{{oidc, "http://idp.example.test/|" + s, ""}} }, false},
		{"no separator", func(s string) []identityLink { return []identityLink{{oidc, s, ""}} }, false},
		{"empty subject", func(s string) []identityLink {
			return []identityLink{{oidc, "https://" + s + ".example.test/|", ""}}
		}, false},
		{"control character in subject", func(s string) []identityLink { return []identityLink{{oidc, testIssuer + "|" + s + "\a", ""}} }, false},
		{"C1 control character in subject", func(s string) []identityLink { return []identityLink{{oidc, testIssuer + "|" + s + "\u0085", ""}} }, false},
		{"subject longer than 255", func(s string) []identityLink {
			return []identityLink{{oidc, testIssuer + "|" + s + strings.Repeat("x", 256-len(s)), ""}}
		}, false},
		{"issuer longer than 500", func(s string) []identityLink {
			return []identityLink{{oidc, "https://idp.example.test/" + strings.Repeat("p", 476) + "|" + s, ""}}
		}, false},
		{"issuer with credentials", func(s string) []identityLink {
			return []identityLink{{oidc, "https://user@idp.example.test/|" + s, ""}}
		}, false},
		{"issuer with a query", func(s string) []identityLink {
			return []identityLink{{oidc, "https://idp.example.test/?tenant=1|" + s, ""}}
		}, false},
		{"issuer with a space", func(s string) []identityLink { return []identityLink{{oidc, "https://idp.example.test/a b|" + s, ""}} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account, _ := tenantAccount(t, pool, tenant)
			sub := uuid.NewString()
			links := tc.links(sub)
			for _, l := range links {
				linkIdentity(t, pool, l.install, account, l.external, l.issuer)
			}
			issuer, subject := "", ""
			if tc.want {
				issuer, subject = testIssuer, strings.TrimPrefix(links[0].external, testIssuer+"|")
			}
			wantIdentity(t, pool, account, issuer, subject)
		})
	}
}

// One sign-in identity held by two accounts identifies neither; removing
// one of the links restores the other's identity.
func TestSharedVerifiedSubjectIdentifiesNeitherAccount(t *testing.T) {
	_, pool, _, _, _, tenant := fixture(t)
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	second := authInstallation(t, pool, "silo.auth.oidc", true)
	first, _ := tenantAccount(t, pool, tenant)
	other, _ := tenantAccount(t, pool, tenant)
	sub := uuid.NewString()
	linkIdentity(t, pool, oidc, first, testIssuer+"|"+sub, "")
	wantIdentity(t, pool, first, testIssuer, sub)
	shared := linkIdentity(t, pool, second, other, testIssuer+"|"+sub, testIssuer)
	wantIdentity(t, pool, first, "", "")
	wantIdentity(t, pool, other, "", "")
	if _, err := pool.Exec(t.Context(), `DELETE FROM plugin_auth_identities WHERE id=$1`, shared); err != nil {
		t.Fatal(err)
	}
	wantIdentity(t, pool, first, testIssuer, sub)
	wantIdentity(t, pool, other, "", "")
	// Sharing through a disabled installation does not count.
	disabled := authInstallation(t, pool, "silo.auth.oidc", false)
	linkIdentity(t, pool, disabled, other, testIssuer+"|"+sub, "")
	wantIdentity(t, pool, first, testIssuer, sub)
	if _, err := pool.Exec(t.Context(), `UPDATE plugin_installations SET enabled=true WHERE id=$1`, disabled); err != nil {
		t.Fatal(err)
	}
	wantIdentity(t, pool, first, "", "")
	wantIdentity(t, pool, other, "", "")
}

func TestVerifiedIdentityFollowsLinksAndInstallations(t *testing.T) {
	_, pool, _, _, _, tenant := fixture(t)
	ctx := t.Context()
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	account, _ := tenantAccount(t, pool, tenant)
	moved, _ := tenantAccount(t, pool, tenant)
	sub := uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	_, _, unlinked := projectedIdentity(t, pool, account)
	link := linkIdentity(t, pool, oidc, account, testIssuer+"|"+sub, "")
	linked := wantIdentity(t, pool, account, testIssuer, sub)
	if linked <= unlinked {
		t.Fatal("linking an identity published no revision")
	}
	exec(`UPDATE plugin_installations SET enabled=false WHERE id=$1`, oidc)
	disabled := wantIdentity(t, pool, account, "", "")
	if disabled <= linked {
		t.Fatal("disabling the installation published no revision")
	}
	exec(`UPDATE plugin_installations SET enabled=true WHERE id=$1`, oidc)
	wantIdentity(t, pool, account, testIssuer, sub)
	exec(`UPDATE plugin_installations SET plugin_id='bloem.auth.renamed' WHERE id=$1`, oidc)
	wantIdentity(t, pool, account, "", "")
	exec(`UPDATE plugin_installations SET plugin_id='silo.auth.oidc' WHERE id=$1`, oidc)
	wantIdentity(t, pool, account, testIssuer, sub)

	// An administrator moves the link to another account.
	exec(`UPDATE plugin_auth_identities SET user_id=$2 WHERE id=$1`, link, moved)
	wantIdentity(t, pool, account, "", "")
	wantIdentity(t, pool, moved, testIssuer, sub)
	// A rewritten key is a different identity.
	resub := uuid.NewString()
	exec(`UPDATE plugin_auth_identities SET external_subject=$2 WHERE id=$1`, link, testIssuer+"|"+resub)
	wantIdentity(t, pool, moved, testIssuer, resub)
	// An issuer column that stops agreeing withdraws the identity.
	exec(`UPDATE plugin_auth_identities SET issuer='https://other.example.test/' WHERE id=$1`, link)
	wantIdentity(t, pool, moved, "", "")
	exec(`UPDATE plugin_auth_identities SET issuer=$2 WHERE id=$1`, link, testIssuer)
	before := wantIdentity(t, pool, moved, testIssuer, resub)
	// Unlinking clears it under a new revision.
	exec(`DELETE FROM plugin_auth_identities WHERE id=$1`, link)
	if after := wantIdentity(t, pool, moved, "", ""); after <= before {
		t.Fatal("unlinking published no revision")
	}

	// Removing an installation removes its identities with it.
	removable := authInstallation(t, pool, "silo.auth.oidc", true)
	linkIdentity(t, pool, removable, account, testIssuer+"|"+sub, testIssuer)
	wantIdentity(t, pool, account, testIssuer, sub)
	exec(`DELETE FROM organization_entitlements WHERE plugin_installation_id=$1`, removable)
	exec(`DELETE FROM entitlement_bundle_members WHERE plugin_installation_id=$1`, removable)
	exec(`DELETE FROM plugin_installations WHERE id=$1`, removable)
	wantIdentity(t, pool, account, "", "")
}

// Sign-ins and provider re-checks rewrite the issuer column of existing
// identities. When the derived identity does not change they must publish
// nothing and never wait on the publication clock.
func TestSignInIssuerRewriteTakesNoRevisionOrClockLock(t *testing.T) {
	_, pool, _, _, _, tenant := fixture(t)
	ctx := t.Context()
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	account, _ := tenantAccount(t, pool, tenant)
	sub := uuid.NewString()
	link := linkIdentity(t, pool, oidc, account, testIssuer+"|"+sub, "")
	before := wantIdentity(t, pool, account, testIssuer, sub)
	clock := clockRevision(t, pool)

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'`); err != nil {
		t.Fatal(err)
	}
	// recordIdentitySignIn: the legacy empty issuer becomes the token's.
	if _, err = tx.Exec(ctx, `UPDATE plugin_auth_identities SET issuer=$2,username='person',email='person@example.test',display_name='Person',
 last_sign_in_at=NOW(),last_checked_at=NOW(),last_check_status='active',last_authenticated_at=NOW(),pending_refusal='',updated_at=NOW() WHERE id=$1`, link, testIssuer); err != nil {
		t.Fatalf("sign-in waited on the publication clock: %v", err)
	}
	// recordIdentityCheck with and without an issuer in the answer.
	for _, answer := range []string{"", testIssuer} {
		if _, err = tx.Exec(ctx, `UPDATE plugin_auth_identities SET last_checked_at=NOW(),last_check_status='active',last_authenticated_at=NOW(),
 issuer=COALESCE(NULLIF($2,''),issuer),pending_refusal='',updated_at=NOW() WHERE id=$1`, link, answer); err != nil {
			t.Fatalf("provider re-check waited on the publication clock: %v", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if after := wantIdentity(t, pool, account, testIssuer, sub); after != before {
		t.Fatalf("sign-in republished the account: revision %d -> %d", before, after)
	}
	if got := clockRevision(t, pool); got != clock {
		t.Fatalf("sign-in advanced the publication clock %d -> %d", clock, got)
	}
}

func inventory(t *testing.T, s *Service, id int) map[string]Profile {
	t.Helper()
	out := map[string]Profile{}
	page, err := s.ListProfiles(t.Context(), id, "", "", 100)
	for {
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page.Profiles {
			out[p.ProfileID] = p
		}
		if page.Complete {
			return out
		}
		page, err = s.ListProfiles(t.Context(), id, page.SnapshotID, page.NextCursor, 100)
	}
}

func TestInventoryAndEnrollmentCarryVerifiedIdentity(t *testing.T) {
	s, pool, owner, ownerProfile, id, tenant := fixture(t)
	s.Cipher, _ = secret.New([]byte(strings.Repeat("fixture-only-key-", 3)))
	ctx := t.Context()
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	sub := uuid.NewString()
	linkIdentity(t, pool, oidc, owner, testIssuer+"|"+sub, "")
	_, otherProfile := tenantAccount(t, pool, tenant)
	profiles := inventory(t, s, id)
	identified, plain := profiles[ownerProfile], profiles[otherProfile]
	if identified.VerifiedIdentityIssuer != testIssuer || identified.VerifiedIdentitySubject != sub {
		t.Fatalf("inventory identity = (%q, %q)", identified.VerifiedIdentityIssuer, identified.VerifiedIdentitySubject)
	}
	if plain.ProfileID == "" || plain.VerifiedIdentityIssuer != "" || plain.VerifiedIdentitySubject != "" {
		t.Fatalf("account without identity exported %+v", plain)
	}
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	enroll := func(p Profile, destination string) error {
		e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: destination, DestinationProfileID: destination, Credential: "ptbs_fixture-" + uuid.NewString(), Active: true, Generation: info.Generation}
		e.ID = EnrollmentID(info.InstanceID, e)
		return s.EnsureConnection(ctx, id, e)
	}
	altered := identified
	altered.VerifiedIdentitySubject = uuid.NewString()
	dropped := identified
	dropped.VerifiedIdentityIssuer, dropped.VerifiedIdentitySubject = "", ""
	injected := plain
	injected.VerifiedIdentityIssuer, injected.VerifiedIdentitySubject = testIssuer, sub
	for name, p := range map[string]Profile{"altered": altered, "dropped": dropped, "injected": injected} {
		if err := enroll(p, "31"); !errors.Is(err, ErrAuthority) {
			t.Fatalf("%s identity: %v, want authority denied", name, err)
		}
	}
	if err := enroll(identified, "31"); err != nil {
		t.Fatalf("exact identity echo: %v", err)
	}
	if err := enroll(plain, "32"); err != nil {
		t.Fatalf("account without identity: %v", err)
	}
}

func brokerClient(t *testing.T, s *Service, id int) managedv1.ManagedTrackingHostClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	s.RegisterBroker(server, "bloem.pastime", id)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return managedv1.NewManagedTrackingHostClient(conn)
}

func TestBrokerForwardsAndChecksVerifiedIdentity(t *testing.T) {
	s, pool, owner, ownerProfile, id, tenant := fixture(t)
	s.Cipher, _ = secret.New([]byte(strings.Repeat("fixture-only-key-", 3)))
	ctx := t.Context()
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	sub := uuid.NewString()
	linkIdentity(t, pool, oidc, owner, testIssuer+"|"+sub, "")
	_, otherProfile := tenantAccount(t, pool, tenant)
	client := brokerClient(t, s, id)
	info, err := client.GetInfo(ctx, &managedv1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	wire := map[string]*managedv1.Profile{}
	page, err := client.ListProfiles(ctx, &managedv1.ListRequest{Limit: 100})
	for {
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page.GetProfiles() {
			wire[p.GetProfileId()] = p
		}
		if page.GetComplete() {
			break
		}
		page, err = client.ListProfiles(ctx, &managedv1.ListRequest{SnapshotId: page.GetSnapshotId(), Cursor: page.GetNextCursor(), Limit: 100})
	}
	identified, plain := wire[ownerProfile], wire[otherProfile]
	if identified.GetVerifiedIdentityIssuer() != testIssuer || identified.GetVerifiedIdentitySubject() != sub {
		t.Fatalf("broker inventory identity = (%q, %q)", identified.GetVerifiedIdentityIssuer(), identified.GetVerifiedIdentitySubject())
	}
	if plain == nil || plain.GetVerifiedIdentityIssuer() != "" || plain.GetVerifiedIdentitySubject() != "" {
		t.Fatalf("broker exported an identity for an account without one: %v", plain)
	}
	ensure := func(p *managedv1.Profile, destination string) error {
		e := Enrollment{Profile: profileDTO(p), ProviderID: "pastime", DestinationAccountID: destination, DestinationProfileID: destination, Credential: "ptbs_fixture-" + uuid.NewString(), Active: true, Generation: info.GetCredentialGeneration()}
		_, err := client.EnsureConnection(ctx, &managedv1.EnrollmentRequest{Profile: p, ProviderId: e.ProviderID, EnrollmentId: EnrollmentID(info.GetInstanceId(), e), DestinationAccountId: destination, DestinationProfileId: destination, Credential: e.Credential, Active: true, CredentialGeneration: e.Generation})
		return err
	}
	with := func(p *managedv1.Profile, issuer, subject string) *managedv1.Profile {
		c := proto.Clone(p).(*managedv1.Profile)
		c.VerifiedIdentityIssuer, c.VerifiedIdentitySubject = issuer, subject
		return c
	}
	for name, p := range map[string]*managedv1.Profile{
		"altered subject":     with(identified, testIssuer, uuid.NewString()),
		"altered issuer":      with(identified, "https://other.example.test/", sub),
		"dropped":             with(identified, "", ""),
		"injected":            with(plain, testIssuer, sub),
		"issuer only":         with(identified, testIssuer, ""),
		"subject only":        with(identified, "", sub),
		"plain http issuer":   with(identified, "http://idp.example.test/", sub),
		"control in subject":  with(identified, testIssuer, sub+"\n"),
		"overlong subject":    with(identified, testIssuer, strings.Repeat("s", 256)),
		"overlong issuer":     with(identified, "https://idp.example.test/"+strings.Repeat("p", 476), sub),
		"issuer only, plain":  with(plain, testIssuer, ""),
		"subject only, plain": with(plain, "", sub),
	} {
		if err := ensure(p, "41"); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: code %v (%v), want PermissionDenied", name, status.Code(err), err)
		}
	}
	if err := ensure(identified, "41"); err != nil {
		t.Fatalf("exact identity echo: %v", err)
	}
	if err := ensure(plain, "42"); err != nil {
		t.Fatalf("account without identity: %v", err)
	}
}

func TestBrokerIdentityShape(t *testing.T) {
	for _, tc := range []struct {
		issuer, subject string
		ok              bool
	}{
		{"", "", true},
		{testIssuer, "0d6f6b38-7d7e-4c2e-9d55-7a1f0b8f1c11", true},
		{testIssuer, strings.Repeat("s", 255), true},
		{"https://idp.example.test/" + strings.Repeat("p", 475), "s", true},
		{testIssuer, "", false},
		{"", "s", false},
		{"http://idp.example.test/", "s", false},
		{"HTTPS://idp.example.test/", "s", false},
		{"https://idp.example.test/" + strings.Repeat("p", 476), "s", false},
		{testIssuer, strings.Repeat("s", 256), false},
		{testIssuer, "s\x00", false},
		{testIssuer, "s\u0085", false},
		{"https://idp.example.test/\t", "s", false},
	} {
		if got := identityShapeValid(tc.issuer, tc.subject); got != tc.ok {
			t.Errorf("identityShapeValid(%q, %q) = %v, want %v", tc.issuer, tc.subject, got, tc.ok)
		}
	}
}

// Enrollments the host recorded before identities existed must replay with
// the same payload digest, or every unchanged profile would conflict forever.
func TestEnrollmentDigestUnchangedWithoutIdentity(t *testing.T) {
	type legacyProfile struct {
		AccountID        string `json:"account_id"`
		ProfileID        string `json:"profile_id"`
		DefaultProfileID string `json:"default_profile_id"`
		AccountName      string `json:"account_name"`
		Name             string `json:"name"`
		Email            string `json:"email"`
		AccountRevision  uint64 `json:"account_revision"`
		Revision         uint64 `json:"revision"`
		AccountActive    bool   `json:"account_active"`
		Active           bool   `json:"active"`
	}
	type legacyEnrollment struct {
		Profile                                                                legacyProfile
		ProviderID, ID, DestinationAccountID, DestinationProfileID, Credential string
		Active                                                                 bool
		Generation                                                             uint64
	}
	f := enrolledFixture(t)
	p := f.e.Profile
	legacy := legacyEnrollment{Profile: legacyProfile{p.AccountID, p.ProfileID, p.DefaultProfileID, p.AccountName, p.Name, p.Email, p.AccountRevision, p.Revision, p.AccountActive, p.Active},
		ProviderID: f.e.ProviderID, ID: f.e.ID, DestinationAccountID: f.e.DestinationAccountID, DestinationProfileID: f.e.DestinationProfileID, Credential: f.e.Credential, Active: f.e.Active, Generation: f.e.Generation}
	want, _ := json.Marshal(legacy)
	got, _ := json.Marshal(f.e)
	if !bytes.Equal(got, want) {
		t.Fatalf("enrollment serialization changed:\n got %s\nwant %s", got, want)
	}
	digest := sha256.Sum256(want)
	var stored []byte
	if err := f.s.Pool.QueryRow(t.Context(), `SELECT payload_digest FROM bloem_managed_enrollments WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3`, f.id, f.account, p.ProfileID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, digest[:]) {
		t.Fatal("stored payload digest differs from the pre-identity digest")
	}
	if err := f.s.EnsureConnection(t.Context(), f.id, f.e); err != nil {
		t.Fatalf("replay: %v", err)
	}
	identified := f.e
	identified.Profile.VerifiedIdentityIssuer, identified.Profile.VerifiedIdentitySubject = testIssuer, "subject"
	raw, _ := json.Marshal(identified)
	if !bytes.Contains(raw, []byte(`"verified_identity_issuer":"`+testIssuer+`","verified_identity_subject":"subject"`)) {
		t.Fatalf("identity missing from serialization: %s", raw)
	}
	if EnrollmentID(f.info.InstanceID, identified) != f.e.ID {
		t.Fatal("identity changed the enrollment ID formula")
	}
}

// concurrently runs first in an open transaction, then starts second, waits
// until second blocks on a lock, and commits first.
func concurrently(t *testing.T, pool *pgxpool.Pool, first func(pgx.Tx), second string, args ...any) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	first(tx)
	done := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, second, args...)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("second writer finished without waiting for the first: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second writer never waited for the first")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

// Concurrent link changes must see each other's commits: two accounts that
// gain one key at the same time end up with no identity, and a change that
// looked identity-neutral before the other commit still republishes.
func TestConcurrentLinkChangesConverge(t *testing.T) {
	_, pool, _, _, _, tenant := fixture(t)
	oidc := authInstallation(t, pool, "silo.auth.oidc", true)
	second := authInstallation(t, pool, "silo.auth.oidc", true)
	insert := `INSERT INTO plugin_auth_identities(plugin_installation_id,external_subject,user_id) VALUES($1,$2,$3)`
	t.Run("shared key", func(t *testing.T) {
		first, _ := tenantAccount(t, pool, tenant)
		other, _ := tenantAccount(t, pool, tenant)
		key := testIssuer + "|" + uuid.NewString()
		concurrently(t, pool, func(tx pgx.Tx) {
			if _, err := tx.Exec(t.Context(), insert, oidc, key, first); err != nil {
				t.Fatal(err)
			}
		}, insert, second, key, other)
		wantIdentity(t, pool, first, "", "")
		wantIdentity(t, pool, other, "", "")
	})
	t.Run("neutral-looking change", func(t *testing.T) {
		account, _ := tenantAccount(t, pool, tenant)
		kept := uuid.NewString()
		linkIdentity(t, pool, oidc, account, testIssuer+"|"+kept, "")
		removed := linkIdentity(t, pool, second, account, testIssuer+"|"+uuid.NewString(), "")
		wantIdentity(t, pool, account, "", "")
		// Removing one of two subjects identifies the account; a third subject
		// added meanwhile looks neutral against the old projection but is not.
		concurrently(t, pool, func(tx pgx.Tx) {
			if _, err := tx.Exec(t.Context(), `DELETE FROM plugin_auth_identities WHERE id=$1`, removed); err != nil {
				t.Fatal(err)
			}
		}, insert, second, testIssuer+"|"+uuid.NewString(), account)
		wantIdentity(t, pool, account, "", "")
	})
}
