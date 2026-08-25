package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

const task5ResourceTenancyPredecessor int64 = 20260812190000

// TestDefaultOrganizationMaterializedMediaScopeParity catches a disconnected
// resource-tenancy materializer, availability store, viewer resolver, or
// catalog/playback consumer. Expected IDs come from roots created before the
// resource-tenancy migration; they are never derived from the resolved scope.
func TestDefaultOrganizationMaterializedMediaScopeParity(t *testing.T) {
	ctx := context.Background()
	pool := newTask5PolicyTestDatabase(t, ctx)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if err := database.MigrateDownTo(ctx, pool, migrations.FS, "sql", task5ResourceTenancyPredecessor); err != nil {
		t.Fatalf("migrate to resource-tenancy predecessor: %v", err)
	}

	var existingFolders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_folders`).Scan(&existingFolders); err != nil {
		t.Fatalf("count predecessor folders: %v", err)
	}
	if existingFolders != 0 {
		t.Fatalf("predecessor fixture has %d media folders, want clean zero", existingFolders)
	}

	preexistingFolderIDs := []int{
		insertTask5Folder(t, ctx, pool, "Task 5 legacy platform alpha", nil),
		insertTask5Folder(t, ctx, pool, "Task 5 legacy platform beta", nil),
	}
	slices.Sort(preexistingFolderIDs)

	var accountID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ('task5-v1-user', 'task5-v1-user@example.test', 'x', 'user', true)
		RETURNING id`).Scan(&accountID); err != nil {
		t.Fatalf("create v1 account: %v", err)
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE organizations
		SET status='active', owner_account_id=$1
		WHERE is_default
		RETURNING id`, accountID).Scan(&organizationID); err != nil {
		t.Fatalf("activate default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'admin')`, organizationID, accountID); err != nil {
		t.Fatalf("create default-organization membership: %v", err)
	}

	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("run resource-tenancy materialization migration: %v", err)
	}
	assertTask5BundleContainsFolders(t, ctx, pool, preexistingFolderIDs)
	assertTask5ActiveEntitlements(t, ctx, pool, organizationID, preexistingFolderIDs)

	group, err := access.NewGroupStore(pool).Create(ctx, organizationID, access.CreateGroupInput{
		Name:               "Task 5 v1 viewers",
		MaxPlaybackQuality: "1080p",
		IsDefault:          true,
	})
	if err != nil {
		t.Fatalf("create organization access group: %v", err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	userStore, err := provider.ForUser(ctx, accountID)
	if err != nil {
		t.Fatalf("open PostgreSQL user store: %v", err)
	}
	const profileID = "task5-v1-profile"
	if err := userStore.CreateProfile(ctx, userstore.Profile{
		ID:                profileID,
		Name:              "Task 5 V1 Profile",
		OrganizationID:    organizationID.String(),
		AccessGroupID:     &group.ID,
		AllowedLibraryIDs: nil,
	}); err != nil {
		t.Fatalf("create organization profile: %v", err)
	}

	tenant, err := tenancy.NewResolver(tenancy.NewStore(pool)).Resolve(ctx, accountID, nil, true)
	if err != nil {
		t.Fatalf("resolve default-organization v1 tenant: %v", err)
	}
	requestCtx := tenancy.WithContext(ctx, tenant)
	engine, err := NewEngine(requestCtx)
	if err != nil {
		t.Fatalf("compile policy engine: %v", err)
	}
	tenantLibraries := resourcetenancy.NewStore(pool)
	resolver := NewViewerResolver(
		auth.NewUserRepository(pool),
		provider,
		nil,
		NewPDP(engine),
		tenantLibraries,
		access.NewGroupStore(pool),
	)
	resolve := func() (access.Scope, error) {
		return resolver.Resolve(requestCtx, access.ResolveInput{
			UserID: accountID, SessionID: "task5-v1-session", ProfileID: profileID,
		})
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM organization_entitlements
		WHERE organization_id=$1 AND media_folder_id=ANY($2)`, organizationID, preexistingFolderIDs); err != nil {
		t.Fatalf("remove compatibility materialization: %v", err)
	}
	assertTask5AvailableFoldersEmpty(t, requestCtx, tenantLibraries, tenant, "missing materialization")
	assertTask5ResolvedScopeEmpty(t, resolve, "missing materialization")

	installTask5MaterializationFailure(t, ctx, pool, organizationID)
	if _, err := resourcetenancy.NewMaterializer(pool).MaterializeDefaultBundle(
		ctx, organizationID, resourcetenancy.Actor{Service: "task5-parity"},
	); !errors.Is(err, resourcetenancy.ErrResourceUnavailable) {
		t.Fatalf("injected materialization error = %v, want ErrResourceUnavailable", err)
	}
	assertTask5AvailableFoldersEmpty(t, requestCtx, tenantLibraries, tenant, "failed materialization")
	assertTask5ResolvedScopeEmpty(t, resolve, "failed materialization")
	dropTask5MaterializationFailure(t, ctx, pool)

	materialized, err := resourcetenancy.NewMaterializer(pool).MaterializeDefaultBundle(
		ctx, organizationID, resourcetenancy.Actor{Service: "task5-parity"},
	)
	if err != nil {
		t.Fatalf("materialize default bundle: %v", err)
	}
	if materialized.Created != int64(len(preexistingFolderIDs)) {
		t.Fatalf("materialized folders = %d, want %d", materialized.Created, len(preexistingFolderIDs))
	}

	nonentitledFolderID := insertTask5Folder(t, ctx, pool, "Task 5 non-entitled platform", nil)
	if _, err := pool.Exec(ctx, `
		DELETE FROM organization_entitlements
		WHERE organization_id=$1 AND media_folder_id=$2`, organizationID, nonentitledFolderID); err != nil {
		t.Fatalf("remove post-materialization compatibility entitlement: %v", err)
	}
	var foreignOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ('task5-foreign', 'Task 5 Foreign', 'active', $1, false)
		RETURNING id`, accountID).Scan(&foreignOrganizationID); err != nil {
		t.Fatalf("create foreign organization: %v", err)
	}
	var foreignOwnerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM resource_owners
		WHERE kind='organization' AND organization_id=$1`, foreignOrganizationID).Scan(&foreignOwnerID); err != nil {
		t.Fatalf("load foreign organization owner: %v", err)
	}
	foreignFolderID := insertTask5Folder(t, ctx, pool, "Task 5 foreign organization", &foreignOwnerID)

	contentByFolder := map[int]string{
		preexistingFolderIDs[0]: "task5-parity-alpha",
		preexistingFolderIDs[1]: "task5-parity-beta",
		nonentitledFolderID:     "task5-parity-unentitled",
		foreignFolderID:         "task5-parity-foreign",
	}
	for folderID, contentID := range contentByFolder {
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_items (content_id, type, title, status, genres)
			VALUES ($1, 'movie', $2, 'matched', '{}'::text[])`,
			contentID, "Task Five Parity "+contentID,
		); err != nil {
			t.Fatalf("create media item %s: %v", contentID, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_item_libraries (content_id, media_folder_id)
			VALUES ($1, $2)`, contentID, folderID); err != nil {
			t.Fatalf("link media item %s to folder %d: %v", contentID, folderID, err)
		}
	}

	scope, err := resolve()
	if err != nil {
		t.Fatalf("resolve materialized viewer scope: %v", err)
	}
	if !scope.LibrariesRestricted || !slices.Equal(scope.AllowedLibraryIDs, preexistingFolderIDs) {
		t.Fatalf("resolved library scope = %#v, want exact pre-existing platform folders %v", scope, preexistingFolderIDs)
	}
	if scope.MaxPlaybackQuality != "1080p" {
		t.Fatalf("resolved group quality = %q, want real group restriction 1080p", scope.MaxPlaybackQuality)
	}

	filter := catalog.AccessFilter{
		AllowedLibraryIDs:  slices.Clone(scope.AllowedLibraryIDs),
		DisabledLibraryIDs: slices.Clone(scope.DisabledLibraryIDs),
		MaxContentRating:   scope.MaxContentRating,
		MaxPlaybackQuality: scope.MaxPlaybackQuality,
		UserID:             scope.UserID,
		ProfileID:          scope.ProfileID,
	}
	assertTask5CatalogAndPlaybackVisibility(
		t,
		requestCtx,
		pool,
		filter,
		contentByFolder,
		preexistingFolderIDs,
		[]string{"task5-parity-alpha", "task5-parity-beta"},
	)
}

func newTask5PolicyTestDatabase(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate disposable database name: %v", err)
	}
	name := "bloem_task5_policy_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	return pool
}

func insertTask5Folder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, ownerID *uuid.UUID) int {
	t.Helper()
	var id int
	if ownerID == nil {
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("create platform folder %q: %v", name, err)
		}
		return id
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', $1, $2) RETURNING id`, name, *ownerID).Scan(&id); err != nil {
		t.Fatalf("create organization folder %q: %v", name, err)
	}
	return id
}

func assertTask5BundleContainsFolders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, folderIDs []int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM entitlement_bundle_members
		WHERE root_kind='media_folder' AND media_folder_id=ANY($1)`, folderIDs).Scan(&count); err != nil {
		t.Fatalf("count pre-existing bundle members: %v", err)
	}
	if count != len(folderIDs) {
		t.Fatalf("pre-existing bundle members = %d, want %d", count, len(folderIDs))
	}
}

func assertTask5ActiveEntitlements(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID uuid.UUID,
	folderIDs []int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM organization_entitlements
		WHERE organization_id=$1
		  AND media_folder_id=ANY($2)
		  AND status='active'`, organizationID, folderIDs).Scan(&count); err != nil {
		t.Fatalf("count pre-existing active entitlements: %v", err)
	}
	if count != len(folderIDs) {
		t.Fatalf("pre-existing active entitlements = %d, want %d", count, len(folderIDs))
	}
}

func assertTask5ResolvedScopeEmpty(t *testing.T, resolve func() (access.Scope, error), stage string) {
	t.Helper()
	scope, err := resolve()
	if err != nil {
		t.Fatalf("resolve %s scope: %v", stage, err)
	}
	if !scope.LibrariesRestricted || scope.AllowedLibraryIDs == nil || len(scope.AllowedLibraryIDs) != 0 {
		t.Fatalf("%s scope = %#v, want restricted non-nil empty allow-list", stage, scope)
	}
}

func assertTask5AvailableFoldersEmpty(
	t *testing.T,
	ctx context.Context,
	store *resourcetenancy.Store,
	tenant tenancy.Context,
	stage string,
) {
	t.Helper()
	ids, err := store.AvailableMediaFolderIDs(ctx, tenant)
	if err != nil {
		t.Fatalf("load %s availability: %v", stage, err)
	}
	if ids == nil || len(ids) != 0 {
		t.Fatalf("%s availability = %v, want non-nil empty list", stage, ids)
	}
}

func installTask5MaterializationFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE task5_materialization_failure (organization_id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create materialization failure fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task5_materialization_failure (organization_id) VALUES ($1)`, organizationID); err != nil {
		t.Fatalf("seed materialization failure fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_task5_materialization()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.media_folder_id IS NOT NULL AND EXISTS (
				SELECT 1 FROM task5_materialization_failure WHERE organization_id=NEW.organization_id
			) THEN
				RAISE EXCEPTION 'injected task 5 materialization failure';
			END IF;
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatalf("create materialization failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER reject_task5_materialization
		BEFORE INSERT ON organization_entitlements
		FOR EACH ROW EXECUTE FUNCTION reject_task5_materialization()`); err != nil {
		t.Fatalf("create materialization failure trigger: %v", err)
	}
}

func dropTask5MaterializationFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_task5_materialization ON organization_entitlements`); err != nil {
		t.Fatalf("drop materialization failure trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION reject_task5_materialization()`); err != nil {
		t.Fatalf("drop materialization failure function: %v", err)
	}
}

func assertTask5CatalogAndPlaybackVisibility(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	filter catalog.AccessFilter,
	contentByFolder map[int]string,
	wantFolderIDs []int,
	wantContentIDs []string,
) {
	t.Helper()

	folders, err := catalog.NewFolderRepository(pool).ListByIDs(ctx, filter.AllowedLibraryIDs)
	if err != nil {
		t.Fatalf("list catalog-visible folders: %v", err)
	}
	gotFolderIDs := make([]int, 0, len(folders))
	for _, folder := range folders {
		gotFolderIDs = append(gotFolderIDs, folder.ID)
	}
	slices.Sort(gotFolderIDs)
	if !slices.Equal(gotFolderIDs, wantFolderIDs) {
		t.Fatalf("catalog-visible folders = %v, want pre-existing folders %v", gotFolderIDs, wantFolderIDs)
	}

	allContentIDs := make([]string, 0, len(contentByFolder))
	for _, contentID := range contentByFolder {
		allContentIDs = append(allContentIDs, contentID)
	}
	slices.Sort(allContentIDs)
	slices.Sort(wantContentIDs)

	visible, err := catalog.NewLibraryItemRepository(pool).FilterAccessibleContentIDs(
		ctx, allContentIDs, filter.AllowedLibraryIDs, filter.DisabledLibraryIDs, filter.MaxContentRating,
	)
	if err != nil {
		t.Fatalf("filter catalog-visible content IDs: %v", err)
	}
	gotContentIDs := make([]string, 0, len(visible))
	for contentID := range visible {
		gotContentIDs = append(gotContentIDs, contentID)
	}
	slices.Sort(gotContentIDs)
	if !slices.Equal(gotContentIDs, wantContentIDs) {
		t.Fatalf("catalog-visible content = %v, want %v", gotContentIDs, wantContentIDs)
	}

	itemRepo := catalog.NewItemRepository(pool)
	searchItems, _, err := itemRepo.Search(ctx, "Task Five Parity", []string{"movie"}, 20, 0, filter)
	if err != nil {
		t.Fatalf("search tenant-visible content: %v", err)
	}
	searchIDs := make([]string, 0, len(searchItems))
	for _, item := range searchItems {
		searchIDs = append(searchIDs, item.ContentID)
	}
	slices.Sort(searchIDs)
	if !slices.Equal(searchIDs, wantContentIDs) {
		t.Fatalf("search-visible content = %v, want %v", searchIDs, wantContentIDs)
	}

	for folderID, contentID := range contentByFolder {
		wantAllowed := slices.Contains(wantFolderIDs, folderID)
		err := itemRepo.EnsureAccessible(ctx, contentID, filter)
		if wantAllowed && err != nil {
			t.Errorf("library predicate denied folder %d content %s: %v", folderID, contentID, err)
		}
		if !wantAllowed && !errors.Is(err, catalog.ErrItemNotFound) {
			t.Errorf("library predicate for folder %d content %s = %v, want ErrItemNotFound", folderID, contentID, err)
		}
		fileAllowed := catalog.FileAllowedByAccess(&models.MediaFile{MediaFolderID: folderID, Resolution: "720p"}, filter)
		if fileAllowed != wantAllowed {
			t.Errorf("playback predicate for folder %d = %t, want %t", folderID, fileAllowed, wantAllowed)
		}
	}
}
