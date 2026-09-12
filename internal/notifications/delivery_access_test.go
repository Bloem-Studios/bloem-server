package notifications

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

// deliveryAccessFixture provisions two organizations, one profile in each,
// and the resource-tenancy rows (resource_owners / organization_entitlements
// / media_folders) needed to exercise deliveryAccessPredicate and the
// fanout recipient join against a real PostgreSQL instance. It never resets
// the shared test database: every row it creates carries a unique per-run
// label and is deleted in dependency order by t.Cleanup.
type deliveryAccessFixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool

	orgA, orgB           uuid.UUID
	ownerA, ownerB       uuid.UUID // resource_owners.id for each organization
	platformOwner        uuid.UUID
	userA, userB         int
	profileA, profileB   string
	accessGroupA         int64
	accessGroupB         int64
	organizationEntities []uuid.UUID // organizations created, for cleanup
}

func newDeliveryAccessFixture(t *testing.T) *deliveryAccessFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	f := &deliveryAccessFixture{t: t, ctx: ctx, pool: pool}

	var platformOwner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind='platform'`).Scan(&platformOwner); err != nil {
		t.Fatalf("load platform resource owner: %v", err)
	}
	f.platformOwner = platformOwner

	f.orgA, f.ownerA, f.userA, f.profileA, f.accessGroupA = f.createTenant("dax-a")
	f.orgB, f.ownerB, f.userB, f.profileB, f.accessGroupB = f.createTenant("dax-b")

	t.Cleanup(func() {
		f.cleanup()
	})
	return f
}

func (f *deliveryAccessFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

// createTenant creates one organization, its owning account, an access
// group, and one profile belonging to it. Returns the organization id, its
// resource_owners.id, the account id, the profile id and the access group id.
func (f *deliveryAccessFixture) createTenant(label string) (orgID, ownerID uuid.UUID, userID int, profileID string, accessGroupID int64) {
	f.t.Helper()
	unique := fmt.Sprintf("%s-%d", label, time.Now().UnixNano())

	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', 'admin', true)
		RETURNING id`, unique, unique+"@example.test").Scan(&userID); err != nil {
		f.t.Fatalf("create %s account: %v", label, err)
	}

	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ($1, $2, 'active', $3, false)
		RETURNING id`, unique, unique, userID).Scan(&orgID); err != nil {
		f.t.Fatalf("create %s organization: %v", label, err)
	}
	f.organizationEntities = append(f.organizationEntities, orgID)

	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'admin')`, orgID, userID); err != nil {
		f.t.Fatalf("create %s membership: %v", label, err)
	}

	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM resource_owners WHERE kind='organization' AND organization_id=$1`, orgID).Scan(&ownerID); err != nil {
		f.t.Fatalf("load %s resource owner: %v", label, err)
	}

	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO access_groups (organization_id, name) VALUES ($1, $2) RETURNING id`,
		orgID, unique).Scan(&accessGroupID); err != nil {
		f.t.Fatalf("create %s access group: %v", label, err)
	}

	profileID = unique + "-profile"
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id)
		VALUES ($1, $2, $3, $4, $5)`, profileID, userID, unique, orgID, accessGroupID); err != nil {
		f.t.Fatalf("create %s profile: %v", label, err)
	}
	return orgID, ownerID, userID, profileID, accessGroupID
}

// createOrgFolder creates a media folder owned directly by the organization
// (ownerID from createTenant): ownership alone authorizes it, no entitlement
// row needed.
func (f *deliveryAccessFixture) createOrgFolder(ownerID uuid.UUID, name string) int {
	f.t.Helper()
	var id int
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', $1, $2) RETURNING id`,
		name, ownerID).Scan(&id); err != nil {
		f.t.Fatalf("create org folder: %v", err)
	}
	return id
}

// createPlatformFolder creates a platform-owned media folder with no
// entitlement for any organization yet.
func (f *deliveryAccessFixture) createPlatformFolder(name string) int {
	f.t.Helper()
	var id int
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, name).Scan(&id); err != nil {
		f.t.Fatalf("create platform folder: %v", err)
	}
	return id
}

// entitle grants organizationID an entitlement of the given status to a
// platform-owned folder.
func (f *deliveryAccessFixture) entitle(organizationID uuid.UUID, folderID int, status string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO organization_entitlements
			(organization_id, entitlement_kind, root_kind, root_owner_id, media_folder_id, status, granted_by_service)
		VALUES ($1, 'library_access', 'media_folder', $2, $3, $4, 'delivery-access-test')
		RETURNING id`,
		organizationID, f.platformOwner, folderID, status).Scan(&id); err != nil {
		f.t.Fatalf("create entitlement: %v", err)
	}
	return id
}

func (f *deliveryAccessFixture) revoke(entitlementID uuid.UUID) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE organization_entitlements SET status='revoked', revoked_at=now() WHERE id=$1`, entitlementID); err != nil {
		f.t.Fatalf("revoke entitlement: %v", err)
	}
}

// createItem inserts a media_items row and links it to the given folders via
// media_item_libraries.
func (f *deliveryAccessFixture) createItem(contentID string, folderIDs ...int) {
	f.t.Helper()
	f.exec(`INSERT INTO media_items (content_id, type, title) VALUES ($1, 'movie', $1)`, contentID)
	for _, folderID := range folderIDs {
		f.exec(`INSERT INTO media_item_libraries (content_id, media_folder_id) VALUES ($1, $2)`, contentID, folderID)
	}
}

// insertDelivery writes a notification_deliveries row directly, bypassing
// BulkInsert so every field combination (including ones production code
// never produces, like an orphaned request.fulfilled with no item) can be
// tested. deliveryType must not be DeliveryTypeEpisodeAvailable unless
// libraryID/seriesID/episodeID are all set (the table's CHECK constraint).
func (f *deliveryAccessFixture) insertDelivery(profileID string, userID int, deliveryType string, libraryID *int, seriesID, episodeID *string) string {
	f.t.Helper()
	id := ulid.Make().String()
	f.exec(`
		INSERT INTO notification_deliveries (id, user_id, profile_id, library_id, series_id, episode_id, type, reason_flags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb)`,
		id, userID, profileID, libraryID, seriesID, episodeID, deliveryType)
	return id
}

func (f *deliveryAccessFixture) cleanup() {
	ctx := context.Background()
	_, _ = f.pool.Exec(ctx, `DELETE FROM notification_deliveries WHERE profile_id = ANY($1)`, []string{f.profileA, f.profileB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM profile_series_interest WHERE profile_id = ANY($1)`, []string{f.profileA, f.profileB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM episodes WHERE series_id LIKE 'dax-%'`)
	_, _ = f.pool.Exec(ctx, `DELETE FROM media_item_libraries WHERE content_id LIKE 'dax-%'`)
	_, _ = f.pool.Exec(ctx, `DELETE FROM media_items WHERE content_id LIKE 'dax-%'`)
	_, _ = f.pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE organization_id = ANY($1)`, f.organizationEntities)
	_, _ = f.pool.Exec(ctx, `DELETE FROM media_folders WHERE owner_id = ANY($1)`, []uuid.UUID{f.ownerA, f.ownerB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM user_profiles WHERE id = ANY($1)`, []string{f.profileA, f.profileB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM organization_memberships WHERE organization_id = ANY($1)`, f.organizationEntities)
	_, _ = f.pool.Exec(ctx, `DELETE FROM access_groups WHERE id = ANY($1)`, []int64{f.accessGroupA, f.accessGroupB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM resource_owners WHERE id = ANY($1)`, []uuid.UUID{f.ownerA, f.ownerB})
	_, _ = f.pool.Exec(ctx, `DELETE FROM organizations WHERE id = ANY($1)`, f.organizationEntities)
	_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []int{f.userA, f.userB})
}

// --- fanout recipient query (profile_series_interest / ListActiveBySeries) ---

// A stale interest row naming another tenant's library must never select
// that tenant's profile as a fanout recipient.
func TestListActiveBySeriesRejectsStaleInterestForAnotherTenantsLibrary(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewInterestRepository(f.pool)

	libraryA := f.createOrgFolder(f.ownerA, "org-a-library")
	libraryB := f.createOrgFolder(f.ownerB, "org-b-library")
	const seriesID = "dax-series-1"

	// profileB's interest row stale-names libraryA (organization A's
	// private library) — e.g. left over from before a library was
	// reassigned, or forged.
	f.exec(`
		INSERT INTO profile_series_interest (user_id, profile_id, library_id, series_id, favorite)
		VALUES ($1, $2, $3, $4, true)`, f.userB, f.profileB, libraryA, seriesID)
	// profileA's own interest row is the control: it must still be selected.
	f.exec(`
		INSERT INTO profile_series_interest (user_id, profile_id, library_id, series_id, favorite)
		VALUES ($1, $2, $3, $4, true)`, f.userA, f.profileA, libraryA, seriesID)

	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()

	got, err := repo.ListActiveBySeries(f.ctx, tx, libraryA, seriesID)
	if err != nil {
		t.Fatalf("ListActiveBySeries: %v", err)
	}
	assertRecipients(t, got, []string{f.profileA})

	// Control: profileB's interest row on its OWN library is still selected
	// when queried against that library — the join rejects the wrong
	// library, not profileB's organization outright.
	f.exec(`
		INSERT INTO profile_series_interest (user_id, profile_id, library_id, series_id, favorite)
		VALUES ($1, $2, $3, $4, true)`, f.userB, f.profileB, libraryB, seriesID)
	gotB, err := repo.ListActiveBySeries(f.ctx, tx, libraryB, seriesID)
	if err != nil {
		t.Fatalf("ListActiveBySeries (own library): %v", err)
	}
	assertRecipients(t, gotB, []string{f.profileB})
}

// A library whose entitlement was revoked after the interest row was
// written must stop selecting that recipient.
func TestListActiveBySeriesRejectsRevokedEntitlement(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewInterestRepository(f.pool)

	platformLibrary := f.createPlatformFolder("shared-platform-library")
	entitlementID := f.entitle(f.orgA, platformLibrary, "active")
	const seriesID = "dax-series-2"

	f.exec(`
		INSERT INTO profile_series_interest (user_id, profile_id, library_id, series_id, favorite)
		VALUES ($1, $2, $3, $4, true)`, f.userA, f.profileA, platformLibrary, seriesID)

	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	got, err := repo.ListActiveBySeries(f.ctx, tx, platformLibrary, seriesID)
	if err != nil {
		t.Fatalf("ListActiveBySeries (active): %v", err)
	}
	assertRecipients(t, got, []string{f.profileA})
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	f.revoke(entitlementID)

	tx2, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	defer func() { _ = tx2.Rollback(t.Context()) }()
	got, err = repo.ListActiveBySeries(f.ctx, tx2, platformLibrary, seriesID)
	if err != nil {
		t.Fatalf("ListActiveBySeries (revoked): %v", err)
	}
	assertRecipients(t, got, nil)
}

func assertRecipients(t *testing.T, got []SeriesInterest, wantProfiles []string) {
	t.Helper()
	gotProfiles := make([]string, 0, len(got))
	for _, interest := range got {
		gotProfiles = append(gotProfiles, interest.ProfileID)
	}
	if len(gotProfiles) != len(wantProfiles) {
		t.Fatalf("recipients = %v, want %v", gotProfiles, wantProfiles)
	}
	want := make(map[string]bool, len(wantProfiles))
	for _, p := range wantProfiles {
		want[p] = true
	}
	for _, p := range gotProfiles {
		if !want[p] {
			t.Fatalf("unexpected recipient %q in %v, want %v", p, gotProfiles, wantProfiles)
		}
	}
}

// --- queued delivery lookup (GetRowByID, used by webhook/webpush/push senders) ---

// A queued delivery read requires an active organization: a suspended
// organization loses access to its own deliveries too.
func TestGetRowByIDRequiresActiveOrganization(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	id := f.insertDelivery(f.profileA, f.userA, DeliveryTypeWebhookAutoDisabled, nil, nil, nil)

	row, err := repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (active org): %v", err)
	}
	if row == nil {
		t.Fatal("GetRowByID (active org) = nil, want a row")
	}

	f.exec(`UPDATE organizations SET status='suspended' WHERE id=$1`, f.orgA)
	t.Cleanup(func() { f.exec(`UPDATE organizations SET status='active' WHERE id=$1`, f.orgA) })

	row, err = repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (suspended org): %v", err)
	}
	if row != nil {
		t.Fatalf("GetRowByID (suspended org) = %+v, want nil", row)
	}
}

// A library-bound notice requires current ownership or an active
// entitlement.
func TestGetRowByIDLibraryBoundRequiresOwnershipOrActiveEntitlement(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	ownedLibrary := f.createOrgFolder(f.ownerA, "owned-library")
	seriesID, episodeID := "dax-owned-series", "dax-owned-episode"
	id := f.insertDelivery(f.profileA, f.userA, "test.library_bound", &ownedLibrary, &seriesID, &episodeID)

	row, err := repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (owned library): %v", err)
	}
	if row == nil {
		t.Fatal("GetRowByID (owned library) = nil, want a row (ownership authorizes it)")
	}

	entitledLibrary := f.createPlatformFolder("entitled-library")
	f.entitle(f.orgA, entitledLibrary, "active")
	seriesID2, episodeID2 := "dax-entitled-series", "dax-entitled-episode"
	id2 := f.insertDelivery(f.profileA, f.userA, "test.library_bound", &entitledLibrary, &seriesID2, &episodeID2)
	row2, err := repo.GetRowByID(f.ctx, id2)
	if err != nil {
		t.Fatalf("GetRowByID (active entitlement): %v", err)
	}
	if row2 == nil {
		t.Fatal("GetRowByID (active entitlement) = nil, want a row")
	}

	// Neither owned nor entitled: another organization's private library.
	foreignLibrary := f.createOrgFolder(f.ownerB, "foreign-library")
	seriesID3, episodeID3 := "dax-foreign-series", "dax-foreign-episode"
	id3 := f.insertDelivery(f.profileA, f.userA, "test.library_bound", &foreignLibrary, &seriesID3, &episodeID3)
	row3, err := repo.GetRowByID(f.ctx, id3)
	if err != nil {
		t.Fatalf("GetRowByID (foreign library): %v", err)
	}
	if row3 != nil {
		t.Fatalf("GetRowByID (foreign library) = %+v, want nil", row3)
	}
}

// A catalog-bound notice with no fixed library requires access to at least
// one library containing the item.
func TestGetRowByIDCatalogBoundRequiresAccessToALibraryContainingItem(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	ownedLibrary := f.createOrgFolder(f.ownerA, "catalog-owned-library")
	itemID := "dax-catalog-item-1"
	f.createItem(itemID, ownedLibrary)

	id := f.insertDelivery(f.profileA, f.userA, DeliveryTypeRequestFulfilled, nil, &itemID, nil)
	row, err := repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (catalog-bound, accessible): %v", err)
	}
	if row == nil {
		t.Fatal("GetRowByID (catalog-bound, accessible) = nil, want a row")
	}
}

// The canonical item remaining in another organization's private library
// does not authorize the recipient, even though the item exists.
func TestGetRowByIDCatalogBoundDeniesItemOnlyInAnotherTenantsLibrary(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	foreignLibrary := f.createOrgFolder(f.ownerB, "catalog-foreign-library")
	itemID := "dax-catalog-item-2"
	f.createItem(itemID, foreignLibrary)

	id := f.insertDelivery(f.profileA, f.userA, DeliveryTypeRequestFulfilled, nil, &itemID, nil)
	row, err := repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (item only in foreign library): %v", err)
	}
	if row != nil {
		t.Fatalf("GetRowByID (item only in foreign library) = %+v, want nil", row)
	}
}

// A fulfilled notice with no item identity is not eligible: it is malformed
// data, not an account-level notice that should fall through to "eligible
// for an active organization".
func TestGetRowByIDFulfilledWithNoItemIdentityIsNotEligible(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	id := f.insertDelivery(f.profileA, f.userA, DeliveryTypeRequestFulfilled, nil, nil, nil)
	row, err := repo.GetRowByID(f.ctx, id)
	if err != nil {
		t.Fatalf("GetRowByID (fulfilled, no item): %v", err)
	}
	if row != nil {
		t.Fatalf("GetRowByID (fulfilled, no item) = %+v, want nil", row)
	}
}

// An account-level notice with neither a library nor an item (request
// approved/declined, system alert, webhook auto-disabled, …) remains
// eligible for an active organization.
func TestGetRowByIDAccountLevelStaysEligibleForActiveOrganization(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	for _, deliveryType := range []string{
		DeliveryTypeRequestApproved,
		DeliveryTypeRequestDeclined,
		DeliveryTypeWebhookAutoDisabled,
		DeliveryTypeSystemAlert,
		DeliveryTypeSystemAnnouncement,
	} {
		id := f.insertDelivery(f.profileA, f.userA, deliveryType, nil, nil, nil)
		row, err := repo.GetRowByID(f.ctx, id)
		if err != nil {
			t.Fatalf("GetRowByID (%s): %v", deliveryType, err)
		}
		if row == nil {
			t.Fatalf("GetRowByID (%s) = nil, want a row", deliveryType)
		}
	}
}

// --- pending-work checks (Has*ForProfileSince / Has*ForUserSince) ---

// The email digest's pending-work check must not report a stale library-bound
// delivery whose entitlement was revoked.
func TestHasForProfileSinceExcludesRevokedLibraryDelivery(t *testing.T) {
	f := newDeliveryAccessFixture(t)
	repo := NewDeliveryRepository(f.pool)

	platformLibrary := f.createPlatformFolder("digest-platform-library")
	entitlementID := f.entitle(f.orgA, platformLibrary, "active")
	seriesID, episodeID := "dax-digest-series", "dax-digest-episode"
	f.insertDelivery(f.profileA, f.userA, "test.library_bound", &platformLibrary, &seriesID, &episodeID)

	since := Cursor{CreatedAt: time.Now().Add(-time.Hour), ID: ""}
	has, err := repo.HasForProfileSince(f.ctx, f.profileA, since)
	if err != nil {
		t.Fatalf("HasForProfileSince (active): %v", err)
	}
	if !has {
		t.Fatal("HasForProfileSince (active) = false, want true")
	}

	f.revoke(entitlementID)

	has, err = repo.HasForProfileSince(f.ctx, f.profileA, since)
	if err != nil {
		t.Fatalf("HasForProfileSince (revoked): %v", err)
	}
	if has {
		t.Fatal("HasForProfileSince (revoked) = true, want false")
	}
}
