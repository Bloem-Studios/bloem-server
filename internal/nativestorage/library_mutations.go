package nativestorage

import (
	"context"
	"errors"
	"slices"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	libraryEditPurpose        = "bloem/library-locations/edit/v1"
	locationMixingUnavailable = "location_mixing_unavailable"
)

type SavedRootSelection struct {
	RootSelection
	Name        string `json:"name"`
	LogicalPath string `json:"logical_path"`
}
type LibraryRootView struct {
	ID          uuid.UUID `json:"id"`
	DirectoryID string    `json:"directory_id"`
	Name        string    `json:"name"`
	LogicalPath string    `json:"logical_path"`
	Enabled     bool      `json:"enabled"`
}
type LibraryLocationView struct {
	ID                    uuid.UUID         `json:"id"`
	SourceKey             uuid.UUID         `json:"source_key"`
	ConfigurationRevision int64             `json:"configuration_revision"`
	SelectionRevision     int64             `json:"selection_revision"`
	Enabled               bool              `json:"enabled"`
	Roots                 []LibraryRootView `json:"roots" nullable:"false"`
}
type ReplaceLocationsRequest struct {
	Paths             []string        `json:"paths" nullable:"false"`
	Selections        []RootSelection `json:"selections" nullable:"false" maxItems:"128"`
	ExpectedRevision  int64           `json:"expected_revision" minimum:"1"`
	ExpectedEditToken string          `json:"expected_edit_token" minLength:"1" maxLength:"4096"`
}

// LocationReplacement is a committed replacement. PreviousPaths are the
// filesystem paths it replaced, read under the same row locks, so callers run
// Silo's path-change follow-up only after a confirmed commit. Respond with the
// embedded LibrarySelection; PreviousPaths is not part of the wire view.
type LocationReplacement struct {
	LibrarySelection
	PreviousPaths []string
}
type PreparedLibraryCreation struct {
	selections *PreparedSelections
	owner      uuid.UUID
	identity   *PreparedLibraryCreation
}
type libraryPathEvidence struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Xmin string `json:"xmin"`
}
type libraryEditReference struct {
	LibraryID   int    `json:"l"`
	Revision    int64  `json:"r"`
	PathsDigest string `json:"p"`
	ActorDigest string `json:"a"`
	ExpiresAt   int64  `json:"e"`
}

// PrepareCreation resolves the creation owner and directories before entering
// Silo's creation transaction. The attachment rechecks that actor and owner.
// filesystem reports whether the request also names filesystem paths.
func (l *LibraryLocations) PrepareCreation(ctx context.Context, a auth.AdminContextClaims, organizationID *uuid.UUID, filesystem bool, roots []RootSelection) (*PreparedLibraryCreation, error) {
	if l == nil || l.browser == nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	tx, err := nativeDomainBegin(ctx, l.pool)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := l.resources.RequireStorageOwnerTx(ctx, tx, a, organizationID)
	if err != nil {
		return nil, nativeDomainMap(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nativeDomainMap(err)
	}
	// Refuse a shape known from the request, its paths included, before
	// opening any provider; the creation transaction repeats the check with
	// the inserted paths.
	if err := l.requireScannableShape(filesystem, nil, rootSourceKeys(roots)); err != nil {
		return nil, err
	}
	prepared, err := l.PrepareSelections(ctx, a, roots)
	if err != nil {
		return nil, err
	}
	p := &PreparedLibraryCreation{selections: prepared, owner: owner.ID}
	p.identity = p
	return p, nil
}
func (l *LibraryLocations) AttachCreationTx(ctx context.Context, tx pgx.Tx, a auth.AdminContextClaims, id int, p *PreparedLibraryCreation) error {
	if l == nil || p == nil || p.identity != p || p.owner == uuid.Nil || p.selections == nil {
		return nativeDomainError("invalid_selection_reference")
	}
	// This newly inserted row is private to the caller's creation transaction.
	// Silo's INSERT grants the default organization access to platform libraries.
	// Remove only that new compatibility grant before assigning an organization
	// owner; its composite FK otherwise correctly forbids changing ownership.
	if _, err := tx.Exec(ctx, `DELETE FROM organization_entitlements e USING media_folders f WHERE f.id=$1 AND e.media_folder_id=f.id AND e.granted_by_service='resource-root-compatibility' AND f.owner_id<>$2`, id, p.owner); err != nil {
		return nativeDomainMap(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE media_folders SET owner_id=$2 WHERE id=$1`, id, p.owner); err != nil {
		return nativeDomainMap(err)
	}
	retained, err := l.RetainPreparedTx(ctx, tx, a, id, p.selections)
	if err != nil {
		return err
	}
	if retained.LibraryOwner.ID != p.owner {
		return nativeDomainError("not_found")
	}
	if err := l.requireSelectionKind(retained.LibraryKind, p.selections); err != nil {
		return err
	}
	var filesystem bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=$1)`, id).Scan(&filesystem); err != nil {
		return nativeDomainMap(err)
	}
	if err := l.requireScannableShape(filesystem, retained.Locations, p.selections.sourceKeys()); err != nil {
		return err
	}
	return l.writeSelectionsTx(ctx, tx, id, p.selections, retained.Locations, false)
}

// requireScannableShape is the single save guard for library shapes the scan
// pipeline cannot handle yet. A library scan reads exactly one retained
// location, then skips filesystem paths and Silo's local cleanup. Until
// MixedLocationsReady, a library therefore uses filesystem paths or one plugin
// source over its lifetime: retained historical locations and replaced paths
// count as well as the requested shape.
func (l *LibraryLocations) requireScannableShape(filesystem bool, retained []storagesource.LocationSelection, requested []uuid.UUID) error {
	if l.MixedLocationsReady() {
		return nil
	}
	sources := map[uuid.UUID]bool{}
	for _, location := range retained {
		sources[location.Location.SourceKey] = true
	}
	for _, key := range requested {
		sources[key] = true
	}
	if len(sources) > 1 || filesystem && len(sources) > 0 {
		return nativeDomainError(locationMixingUnavailable)
	}
	return nil
}
func rootSourceKeys(roots []RootSelection) []uuid.UUID {
	keys := make([]uuid.UUID, 0, len(roots))
	for _, r := range roots {
		keys = append(keys, r.SourceKey)
	}
	return keys
}
func (p *PreparedSelections) sourceKeys() []uuid.UUID {
	keys := make([]uuid.UUID, 0, len(p.roots))
	for _, r := range p.roots {
		keys = append(keys, r.source.Key)
	}
	return keys
}

// nativeMediaKind maps Silo's free-text library type to the canonical media
// kind providers declare. librarykind owns the type vocabulary and aliases.
func nativeMediaKind(libraryType string) string {
	switch {
	case librarykind.IsEbook(libraryType):
		return "ebooks"
	case librarykind.IsMovie(libraryType):
		return "movies"
	case librarykind.IsAudiobook(libraryType):
		return "audiobooks"
	}
	return ""
}
func (l *LibraryLocations) requireSelectionKind(libraryType string, p *PreparedSelections) error {
	if len(p.roots) == 0 {
		return nil
	}
	kind := nativeMediaKind(libraryType)
	admitted := kind == "ebooks" || kind == "movies" && l.nativeMovieReady || kind == "audiobooks" && l.nativeAudioReady
	if !admitted {
		return nativeDomainError("native_storage_unavailable")
	}
	for _, r := range p.roots {
		kinds := r.mediaKinds
		if len(kinds) == 0 {
			kinds = []string{"ebooks"}
		} // Existing ebook protocol providers.
		if !slices.Contains(kinds, kind) {
			return nativeDomainError("invalid_request")
		}
	}
	return nil
}
func libraryPathsTx(ctx context.Context, tx pgx.Tx, id int) ([]libraryPathEvidence, error) {
	// The aggregate has retained the parent FOR UPDATE. FK insertion is fenced;
	// NOWAIT breaks the inversion with a legacy DELETE that owns a path first.
	rows, err := tx.Query(ctx, `SELECT id,path,xmin::text FROM media_folder_paths WHERE media_folder_id=$1 ORDER BY id FOR UPDATE NOWAIT`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[libraryPathEvidence])
}
func libraryRevisionTx(ctx context.Context, tx pgx.Tx, id int) (int64, error) {
	var revision int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(s.revision,1) FROM media_folders f LEFT JOIN bloem_library_location_state s ON s.folder_id=f.id WHERE f.id=$1`, id).Scan(&revision)
	return revision, err
}
func locationRevisionConflict(revision int64) error {
	return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &revision}
}
func pathLockUnavailable(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "55P03"
}
func (l *LibraryLocations) retainedRevision(ctx context.Context, a auth.AdminContextClaims, id int) (int64, error) {
	tx, err := nativeDomainBegin(ctx, l.pool)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = l.resources.RetainNativeResourcesTx(ctx, tx, &a, resourcetenancy.NativeResourceSet{Library: &resourcetenancy.NativeLibraryExpectation{FolderID: id}})
	if err != nil {
		return 0, nativeDomainMap(err)
	}
	return libraryRevisionTx(ctx, tx, id)
}

// ReadSelections projects the full retained location set and fresh actor-bound
// edit evidence. Historical disabled roots remain visible for removal.
func (l *LibraryLocations) ReadSelections(ctx context.Context, a auth.AdminContextClaims, id int) (LibrarySelection, error) {
	if l == nil || l.browser == nil {
		return LibrarySelection{}, nativeDomainError("native_storage_unavailable")
	}
	tx, err := nativeDomainBegin(ctx, l.pool)
	if err != nil {
		return LibrarySelection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	retained, err := l.resources.RetainNativeResourcesTx(ctx, tx, &a, resourcetenancy.NativeResourceSet{Library: &resourcetenancy.NativeLibraryExpectation{FolderID: id}})
	if err != nil {
		return LibrarySelection{}, nativeDomainMap(err)
	}
	paths, err := libraryPathsTx(ctx, tx, id)
	if pathLockUnavailable(err) {
		if rollback := tx.Rollback(ctx); rollback != nil {
			return LibrarySelection{}, nativeDomainMap(rollback)
		}
		revision, e := l.retainedRevision(ctx, a, id)
		if e != nil {
			return LibrarySelection{}, e
		}
		return LibrarySelection{}, locationRevisionConflict(revision)
	}
	if err != nil {
		return LibrarySelection{}, nativeDomainMap(err)
	}
	view, err := l.selectionViewTx(ctx, tx, a, id, paths, retained)
	if err != nil {
		return LibrarySelection{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LibrarySelection{}, nativeDomainMap(err)
	}
	return view, nil
}
func (l *LibraryLocations) selectionViewTx(ctx context.Context, tx pgx.Tx, a auth.AdminContextClaims, id int, paths []libraryPathEvidence, retained resourcetenancy.NativeRetainedResources) (LibrarySelection, error) {
	view := LibrarySelection{Paths: make([]string, 0, len(paths)), Locations: make([]LibraryLocationView, 0, len(retained.Locations)), Selections: make([]SavedRootSelection, 0)}
	var err error
	view.Revision, err = libraryRevisionTx(ctx, tx, id)
	if err != nil {
		return view, nativeDomainMap(err)
	}
	for _, p := range paths {
		view.Paths = append(view.Paths, p.Path)
	}
	expiry := l.browser.now().Add(maxBrowseLifetime)
	if a.ExpiresAt.Before(expiry) {
		expiry = a.ExpiresAt
	}
	ref := libraryEditReference{LibraryID: id, Revision: view.Revision, PathsDigest: digestBrowse(paths), ActorDigest: digestBrowse(a), ExpiresAt: expiry.Unix()}
	if ref.PathsDigest == "" || ref.ActorDigest == "" || ref.ExpiresAt <= l.browser.now().Unix() {
		return view, nativeDomainError("authorization_state_stale")
	}
	view.EditToken, err = l.browser.sealBrowse(ref, libraryEditPurpose)
	if err != nil {
		return view, err
	}
	sources := map[uuid.UUID]resourcetenancy.NativeSourceExpectation{}
	for _, s := range retained.Sources {
		sources[s.Source.Key] = s
	}
	for _, location := range retained.Locations {
		source := sources[location.Location.SourceKey]
		v := LibraryLocationView{ID: location.Location.ID, SourceKey: location.Location.SourceKey, ConfigurationRevision: source.Source.ConfigurationRevision, SelectionRevision: location.Location.SelectionRevision, Enabled: source.Source.Enabled, Roots: make([]LibraryRootView, 0, len(location.Roots))}
		for _, root := range location.Roots {
			v.Roots = append(v.Roots, LibraryRootView{ID: root.ID, DirectoryID: root.DirectoryID, Name: root.Name, LogicalPath: root.LogicalPath, Enabled: root.Enabled})
			if root.Enabled {
				saved := SavedRootSelection{RootSelection: RootSelection{SourceKey: source.Source.Key, ConfigurationRevision: source.Source.ConfigurationRevision}, Name: root.Name, LogicalPath: root.LogicalPath}
				if source.Source.Enabled && source.Source.InstallationID != nil && source.Generation > 0 {
					directory := l.browser.newReference(a, source.Source, source.Generation)
					if root.DirectoryID != source.Source.RootEntryID {
						directory.Ancestry = append(directory.Ancestry, root.DirectoryID)
					}
					saved.SelectionRef, err = l.browser.sealBrowse(directory, selectionPurpose)
					if err != nil {
						return view, err
					}
				}
				view.Selections = append(view.Selections, saved)
			}
		}
		view.Locations = append(view.Locations, v)
	}
	return view, nil
}

// ReplaceSelections performs provider I/O first, then retains the actor and
// complete old/new resource aggregate exactly once before any write.
func (l *LibraryLocations) ReplaceSelections(ctx context.Context, a auth.AdminContextClaims, id int, req ReplaceLocationsRequest) (LocationReplacement, error) {
	if req.ExpectedRevision <= 0 || req.ExpectedEditToken == "" || req.Paths == nil || req.Selections == nil || len(req.Paths)+len(req.Selections) == 0 {
		return LocationReplacement{}, nativeDomainError("invalid_request")
	}
	// Establish library visibility before opening any requested provider.
	if _, err := l.retainedRevision(ctx, a, id); err != nil {
		return LocationReplacement{}, err
	}
	// The transaction repeats this with retained locations and current paths.
	if err := l.requireScannableShape(len(req.Paths) > 0, nil, rootSourceKeys(req.Selections)); err != nil {
		return LocationReplacement{}, err
	}
	p, err := l.PrepareSelections(ctx, a, req.Selections)
	if err != nil {
		return LocationReplacement{}, err
	}
	tx, err := nativeDomainBegin(ctx, l.pool)
	if err != nil {
		return LocationReplacement{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	retained, err := l.RetainPreparedTx(ctx, tx, a, id, p)
	if err != nil {
		return LocationReplacement{}, err
	}
	paths, err := libraryPathsTx(ctx, tx, id)
	if pathLockUnavailable(err) {
		if rollback := tx.Rollback(ctx); rollback != nil {
			return LocationReplacement{}, nativeDomainMap(rollback)
		}
		revision, e := l.retainedRevision(ctx, a, id)
		if e != nil {
			return LocationReplacement{}, e
		}
		return LocationReplacement{}, locationRevisionConflict(revision)
	}
	if err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	revision, err := libraryRevisionTx(ctx, tx, id)
	if err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	// Path rows carry the legacy-write evidence. A path set that is empty when
	// read and empty again here has no row to compare; that interval cannot
	// overwrite a different committed path set (see the operation description).
	var token libraryEditReference
	if l.browser.openBrowse(req.ExpectedEditToken, libraryEditPurpose, &token) != nil || token.LibraryID != id || token.Revision != revision || req.ExpectedRevision != revision || token.PathsDigest == "" || token.ActorDigest == "" || token.PathsDigest != digestBrowse(paths) || token.ActorDigest != digestBrowse(a) || token.ExpiresAt <= l.browser.now().Unix() || token.ExpiresAt > a.ExpiresAt.Unix() {
		return LocationReplacement{}, locationRevisionConflict(revision)
	}
	if err := l.requireSelectionKind(retained.LibraryKind, p); err != nil {
		return LocationReplacement{}, err
	}
	if err := l.requireScannableShape(len(paths) > 0 || len(req.Paths) > 0, retained.Locations, p.sourceKeys()); err != nil {
		return LocationReplacement{}, err
	}
	previous := make([]string, 0, len(paths))
	for _, path := range paths {
		previous = append(previous, path.Path)
	}
	if err := catalog.NewFolderRepository(l.pool).UpdateTx(ctx, tx, id, catalog.UpdateFolderInput{Paths: &req.Paths}); err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	if err := l.writeSelectionsTx(ctx, tx, id, p, retained.Locations, true); err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	// Reload the owned graph in this transaction without acquiring resources again.
	retained.Locations, err = l.readLocationsTx(ctx, tx, id)
	if err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	paths, err = libraryPathsTx(ctx, tx, id)
	if err != nil {
		return LocationReplacement{}, nativeDomainMap(err)
	}
	view, err := l.selectionViewTx(ctx, tx, a, id, paths, retained)
	if err != nil {
		return LocationReplacement{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LocationReplacement{}, &catalog.MutationOutcomeUnknown{OperationID: uuid.New(), LibraryID: id, Operation: "replace_library_locations", Cause: err}
	}
	return LocationReplacement{LibrarySelection: view, PreviousPaths: previous}, nil
}
func (l *LibraryLocations) writeSelectionsTx(ctx context.Context, tx pgx.Tx, id int, p *PreparedSelections, current []storagesource.LocationSelection, bump bool) error {
	locations := map[uuid.UUID]uuid.UUID{}
	for _, v := range current {
		locations[v.Location.SourceKey] = v.Location.ID
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id IN(SELECT id FROM library_storage_locations WHERE folder_id=$1)`, id); err != nil {
		return err
	}
	for _, r := range p.roots {
		location, ok := locations[r.source.Key]
		if !ok {
			location = uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, location, r.source.Key, id); err != nil {
				return err
			}
			locations[r.source.Key] = location
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path,enabled) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT(location_id,directory_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,enabled=true`, uuid.New(), location, r.directoryID, r.name, r.logicalPath); err != nil {
			return err
		}
	}
	if bump {
		if _, err := tx.Exec(ctx, `UPDATE library_storage_locations SET selection_revision=selection_revision+1 WHERE folder_id=$1`, id); err != nil {
			return err
		}
	}
	// Revocation is immediate; scanning can republish a re-enabled membership.
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_file_roots m SET enabled=false FROM bloem_storage_selected_roots r WHERE m.location_id=r.location_id AND m.root_id=r.id AND NOT r.enabled AND r.location_id IN(SELECT id FROM library_storage_locations WHERE folder_id=$1)`, id); err != nil {
		return err
	}
	delta := int64(0)
	if bump {
		delta = 1
	}
	_, err := tx.Exec(ctx, `INSERT INTO bloem_library_location_state(folder_id,revision) VALUES($1,1+$2) ON CONFLICT(folder_id) DO UPDATE SET revision=bloem_library_location_state.revision+$2`, id, delta)
	return err
}
func (l *LibraryLocations) readLocationsTx(ctx context.Context, tx pgx.Tx, id int) ([]storagesource.LocationSelection, error) {
	rows, err := tx.Query(ctx, `SELECT l.id,l.source_key,l.folder_id,l.selection_revision,r.id,r.directory_id,r.name,r.logical_path,r.enabled FROM library_storage_locations l LEFT JOIN bloem_storage_selected_roots r ON r.location_id=l.id WHERE l.folder_id=$1 ORDER BY l.id,r.directory_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]storagesource.LocationSelection, 0)
	for rows.Next() {
		var loc storagesource.Location
		var rootID *uuid.UUID
		var dir, name, path *string
		var enabled *bool
		if err := rows.Scan(&loc.ID, &loc.SourceKey, &loc.FolderID, &loc.SelectionRevision, &rootID, &dir, &name, &path, &enabled); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Location.ID != loc.ID {
			out = append(out, storagesource.LocationSelection{Location: loc, Roots: []storagesource.SelectedRoot{}})
		}
		if rootID != nil {
			out[len(out)-1].Roots = append(out[len(out)-1].Roots, storagesource.SelectedRoot{ID: *rootID, LocationID: loc.ID, DirectoryID: *dir, Name: *name, LogicalPath: *path, Enabled: *enabled})
		}
	}
	return out, rows.Err()
}
