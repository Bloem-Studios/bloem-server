package sections

import (
	"context"
	"errors"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
)

const nativeStorageUnavailableCode = "native_storage_unavailable"

// NativeLibraryAuthorizeTx must retain authority in the current mutation attempt.
// The callback must not commit the transaction.
type NativeLibraryAuthorizeTx func(context.Context, pgx.Tx) error

func (r *Repository) SeedNativeLibraryDefaultsAuthorized(ctx context.Context, libraryID int, authorize NativeLibraryAuthorizeTx) error {
	if r == nil || r.pool == nil || libraryID <= 0 || authorize == nil {
		return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	defaults := DefaultLibrarySectionsForType(&libraryID, "ebook")
	m := scopeMutation("library", &libraryID)
	m.incoming = defaults
	return r.mutate(ctx, m, func(attemptCtx context.Context) error {
		tx := sectionTransaction(attemptCtx)
		if tx == nil {
			return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
		}
		if err := authorize(attemptCtx, tx); err != nil {
			return err
		}
		return r.seedDefaults(attemptCtx, "library", &libraryID, defaults)
	})
}

func (r *Repository) SeedNativeHomeRecentAuthorized(ctx context.Context, libraryID int, name string, authorize NativeLibraryAuthorizeTx) error {
	if r == nil || r.pool == nil || libraryID <= 0 || authorize == nil {
		return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	m := scopeMutation("home", nil)
	m.incoming = generatedHomeLibraryRecentDefaults(libraryID, name, "ebook")
	return r.mutate(ctx, m, func(attemptCtx context.Context) error {
		tx := sectionTransaction(attemptCtx)
		if tx == nil {
			return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
		}
		if err := authorize(attemptCtx, tx); err != nil {
			return err
		}
		_, err := r.createGeneratedHomeLibraryRecentSections(attemptCtx, libraryID, name, "ebook")
		return err
	})
}

// RequireNativeInitializationWitnessesTx validates retained rows in the caller's
// authorized finalization transaction. It neither seeds sections nor changes the
// library marker. Locks remain held until that transaction ends.
func (r *Repository) RequireNativeInitializationWitnessesTx(ctx context.Context, tx pgx.Tx, libraryID int) error {
	if tx == nil || libraryID <= 0 {
		return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	incomplete := func() error { return &catalog.NativeOnboardingError{Code: "initialization_incomplete"} }
	readError := func(err error) error {
		if errors.Is(err, pgx.ErrNoRows) {
			return incomplete()
		}
		return catalog.MapNativeOnboardingError(err)
	}
	var groupID string
	if err := tx.QueryRow(ctx, `SELECT id FROM library_collection_groups
 WHERE id=$1 AND library_id=$2 AND kind='user_collections' AND label=$3
 FOR SHARE NOWAIT`, catalog.CanonicalUserCollectionsGroupID(libraryID), libraryID,
		catalog.CanonicalUserCollectionsGroupLabel).Scan(&groupID); err != nil {
		return readError(err)
	}
	var groupRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM library_collection_order_revisions
 WHERE library_id=$1 FOR SHARE NOWAIT`, libraryID).Scan(&groupRevision); err != nil {
		return readError(err)
	}
	if groupRevision <= 0 {
		return incomplete()
	}

	rows, err := tx.Query(ctx, "SELECT "+sectionColumns+` FROM page_sections
 WHERE (scope='library' AND library_id=$1)
 OR (scope='home' AND library_id IS NULL AND config->>'generated_source'=$2
 AND (config->>'generated_library_id'=$3 OR config->>'filter_library_id'=$3))
 ORDER BY id FOR SHARE NOWAIT`, libraryID, GeneratedHomeLibraryRecentSource, strconv.Itoa(libraryID))
	if err != nil {
		return readError(err)
	}
	sections, err := scanSections(rows)
	rows.Close()
	if err != nil {
		return readError(err)
	}
	ids := make([]string, 0, len(sections))
	libraryPresent, added, released := false, false, false
	for _, s := range sections {
		ids = append(ids, s.ID)
		if s.Scope == "library" && s.Enabled {
			libraryPresent = true
		}
		if !s.Enabled || !IsGeneratedHomeLibraryRecentSection(s, libraryID) {
			continue
		}
		switch s.SectionType {
		case SectionRecentlyAdded:
			added = generatedHomeLibraryRecentKindForSection(s) == generatedHomeLibraryRecentKindAdded || added
		case SectionRecentlyReleased:
			released = generatedHomeLibraryRecentKindForSection(s) == generatedHomeLibraryRecentKindReleased || released
		}
	}
	if !libraryPresent || !added || !released {
		return incomplete()
	}
	rows, err = tx.Query(ctx, `SELECT section_id,revision FROM page_section_revisions
 WHERE section_id=ANY($1) ORDER BY section_id FOR SHARE NOWAIT`, ids)
	if err != nil {
		return readError(err)
	}
	valid := 0
	for rows.Next() {
		var id string
		var revision int64
		if err := rows.Scan(&id, &revision); err != nil {
			rows.Close()
			return readError(err)
		}
		if revision > 0 {
			valid++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return readError(err)
	}
	if valid != len(ids) {
		return incomplete()
	}
	rows, err = tx.Query(ctx, `SELECT scope,revision FROM page_section_scope_revisions
 WHERE (scope='library' AND library_id=$1) OR (scope='home' AND library_id=0)
 ORDER BY scope,library_id FOR SHARE NOWAIT`, libraryID)
	if err != nil {
		return readError(err)
	}
	valid = 0
	for rows.Next() {
		var scope string
		var revision int64
		if err := rows.Scan(&scope, &revision); err != nil {
			rows.Close()
			return readError(err)
		}
		if revision > 0 {
			valid++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return readError(err)
	}
	if valid != 2 {
		return incomplete()
	}
	return nil
}
