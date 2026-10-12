package bookrequests

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AudiobookRequestPluginID is the only plugin the audiobook lane dispatches to.
const AudiobookRequestPluginID = "bloem.requests.audiobooks"

// audiobookIdentityFrom selects an integration's identity only; no
// configuration digest (D3). A platform-owned integration serves the default
// organization; an organization-owned one serves its own organization (D1).
// The media types must be exactly audiobook, and the config may not carry
// Silo's service_kind, which would create a movie/series fallback route.
const audiobookIdentityFrom = `
 FROM request_integrations ri
 JOIN plugin_installations pi ON pi.id=ri.installation_id AND pi.enabled AND pi.kind='plugin' AND pi.plugin_id='` + AudiobookRequestPluginID + `'
 JOIN plugin_capabilities pc ON pc.plugin_installation_id=pi.id AND pc.capability_type='request_router.v1' AND pc.capability_id=ri.capability_id
 JOIN resource_owners ro ON ro.id=pi.owner_id
 JOIN organizations o ON o.id=$1 AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 WHERE ri.enabled AND ri.supported_media_types=ARRAY['audiobook']::text[] AND ri.api_key_ref<>''
 AND NOT (ri.plugin_config ? 'service_kind')
 AND ((ro.kind='organization' AND ro.organization_id=o.id) OR (ro.kind='platform' AND o.is_default))`

const audiobookIdentityColumns = `SELECT ri.id,ri.name,pi.id,ri.capability_id,pi.owner_id`

// audiobookLibraryOwnership mirrors findLibraryMatch: an organization-owned
// folder, or a platform folder with an active entitlement for $1.
const audiobookLibraryOwnership = `((ro.kind='organization' AND ro.organization_id=$1)
 OR (ro.kind='platform' AND EXISTS(SELECT 1 FROM organization_entitlements e WHERE e.organization_id=$1 AND e.media_folder_id=f.id AND e.root_owner_id=ro.id AND e.status='active' FOR SHARE)))`

const audiobookMaxRootFiles = 10000

// AudiobookIntegrationSummary is the admin view of one acquisition backend.
type AudiobookIntegrationSummary struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	InstallationID  int      `json:"installation_id"`
	CapabilityID    string   `json:"capability_id"`
	LibraryID       int      `json:"library_id"`
	LibraryName     string   `json:"library_name,omitempty"`
	DownloadRoots   []string `json:"download_roots"`
	BindingRevision int64    `json:"binding_revision"`
	Ready           bool     `json:"ready"`
}

// AudiobookLibrary is an eligible library for a binding, with its paths.
type AudiobookLibrary struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Paths    []string `json:"paths"`
}

func scanAudiobookIdentity(row pgx.Row, org uuid.UUID) (AudiobookIntegration, error) {
	in := AudiobookIntegration{OrganizationID: org, DownloadRoots: []string{}}
	err := row.Scan(&in.ID, &in.Name, &in.InstallationID, &in.CapabilityID, &in.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrConflict
	}
	return in, err
}

// loadAudiobookIdentity returns a live integration's identity without its
// binding: removing the binding (or the library) must not fail a download
// that is already running.
func loadAudiobookIdentity(ctx context.Context, tx pgx.Tx, org uuid.UUID, id string) (AudiobookIntegration, error) {
	return scanAudiobookIdentity(tx.QueryRow(ctx, audiobookIdentityColumns+audiobookIdentityFrom+` AND ri.id=$2 FOR SHARE OF ri,pi,pc,ro,o`, org, id), org)
}

// loadAudiobookIntegration returns the identity plus a currently valid
// binding (D5: integration plus binding is the opt-in).
func loadAudiobookIntegration(ctx context.Context, tx pgx.Tx, org uuid.UUID, id string) (AudiobookIntegration, error) {
	in, err := loadAudiobookIdentity(ctx, tx, org, id)
	if err != nil {
		return in, err
	}
	var roots []string
	err = tx.QueryRow(ctx, `SELECT media_folder_id,download_roots FROM bloem_audiobook_request_integrations WHERE integration_id=$1 AND organization_id=$2 FOR SHARE`, id, org).Scan(&in.LibraryID, &roots)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrConflict
	}
	if err != nil {
		return in, err
	}
	library, err := loadAudiobookLibrary(ctx, tx, org, in.LibraryID)
	if err != nil {
		return in, err
	}
	if validDownloadRoots(roots, library.Paths) != nil {
		return in, ErrConflict
	}
	in.DownloadRoots, in.Language = roots, library.Language
	return in, nil
}

// selectAudiobookIntegration picks the lowest-ID integration with a valid
// binding. Existing dispatches never move to a sibling backend.
func selectAudiobookIntegration(ctx context.Context, tx pgx.Tx, org uuid.UUID) (AudiobookIntegration, error) {
	rows, err := tx.Query(ctx, `SELECT ri.id`+audiobookIdentityFrom+` AND EXISTS(SELECT 1 FROM bloem_audiobook_request_integrations b WHERE b.integration_id=ri.id AND b.organization_id=o.id) ORDER BY ri.id LIMIT 100`, org)
	if err != nil {
		return AudiobookIntegration{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return AudiobookIntegration{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AudiobookIntegration{}, err
	}
	for _, id := range ids {
		in, err := loadAudiobookIntegration(ctx, tx, org, id)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
			continue
		}
		return in, err
	}
	return AudiobookIntegration{}, ErrConflict
}

func loadAudiobookLibrary(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, org uuid.UUID, id int) (AudiobookLibrary, error) {
	library := AudiobookLibrary{ID: id, Paths: []string{}}
	err := q.QueryRow(ctx, `SELECT f.name,f.metadata_language FROM media_folders f JOIN resource_owners ro ON ro.id=f.owner_id
 WHERE f.id=$2 AND f.enabled AND lower(btrim(f.type)) IN ('audiobook','audiobooks') AND `+audiobookLibraryOwnership+`
 FOR SHARE OF f`, org, id).Scan(&library.Name, &library.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return library, ErrInvalid
	}
	if err != nil {
		return library, err
	}
	if library.Language == "" {
		library.Language = "en"
	}
	rows, err := q.Query(ctx, `SELECT path FROM media_folder_paths WHERE media_folder_id=$1 ORDER BY id`, id)
	if err != nil {
		return library, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			return library, err
		}
		library.Paths = append(library.Paths, path)
	}
	return library, rows.Err()
}

// validDownloadRoots requires dedicated, absolute, clean roots inside the
// bound library's paths. Path completion scans every file below a root.
func validDownloadRoots(roots, folderPaths []string) error {
	if len(roots) < 1 || len(roots) > 4 {
		return fmt.Errorf("%w: provide one to four download folders", ErrInvalid)
	}
	for i, root := range roots {
		if root == "" || len(root) > 1024 || !utf8.ValidString(root) || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || strings.ContainsFunc(root, unicode.IsControl) {
			return fmt.Errorf("%w: download folders must be clean absolute paths", ErrInvalid)
		}
		inside := false
		for _, folder := range folderPaths {
			folder = filepath.Clean(folder)
			if root == folder || strings.HasPrefix(root, strings.TrimSuffix(folder, "/")+"/") {
				inside = true
				break
			}
		}
		if !inside {
			return fmt.Errorf("%w: each download folder must be inside the library's folders", ErrInvalid)
		}
		for j, other := range roots {
			if i != j && (root == other || strings.HasPrefix(root, other+"/")) {
				return fmt.Errorf("%w: download folders must be distinct and not nested", ErrInvalid)
			}
		}
	}
	return nil
}

// checkAudiobookAuthority admits an enabled account with an active
// membership in an active organization; ordinary viewers also need a profile
// in that organization. Unlike ebooks, hosted organizations are admitted.
func checkAudiobookAuthority(ctx context.Context, q querier, v Viewer) error {
	if v.UserID <= 0 || v.OrganizationID == uuid.Nil || !v.Admin && v.ProfileID == "" {
		return ErrForbidden
	}
	var one int
	err := q.QueryRow(ctx, `
 SELECT 1 FROM organizations o
 JOIN users u ON u.id=$1 AND u.enabled
 JOIN organization_memberships m ON m.organization_id=o.id AND m.account_id=u.id AND m.status='active'
 WHERE o.id=$2 AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 AND ($3 OR EXISTS(SELECT 1 FROM user_profiles p WHERE p.user_id=$1 AND p.id=$4 AND p.organization_id=$2 FOR SHARE))
 FOR SHARE OF o,u,m`, v.UserID, v.OrganizationID, v.Admin, v.ProfileID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func audiobookRequestsEnabled(ctx context.Context, q querier) error {
	var enabled bool
	err := q.QueryRow(ctx, `SELECT requests_enabled FROM request_settings WHERE id=true FOR SHARE`).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
		return ErrDisabled
	}
	return err
}

// AudiobookAcquisitionAvailable reports whether org has an integration with
// a binding to an eligible library.
func AudiobookAcquisitionAvailable(ctx context.Context, q querier, org uuid.UUID) (bool, error) {
	var available bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1`+audiobookIdentityFrom+` AND EXISTS(
 SELECT 1 FROM bloem_audiobook_request_integrations b JOIN media_folders f ON f.id=b.media_folder_id AND f.enabled AND lower(btrim(f.type)) IN ('audiobook','audiobooks')
 JOIN resource_owners ro ON ro.id=f.owner_id
 WHERE b.integration_id=ri.id AND b.organization_id=o.id AND `+audiobookLibraryOwnership+`))`, org).Scan(&available)
	return available, err
}

// ListAudiobookIntegrations returns the integrations visible to the
// organization with their bindings. Authority is checked by the caller and
// again here for membership.
func (s *Service) ListAudiobookIntegrations(ctx context.Context, v Viewer) ([]AudiobookIntegrationSummary, error) {
	if !v.Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = checkAudiobookAuthority(ctx, tx, v); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, audiobookIdentityColumns+audiobookIdentityFrom+` ORDER BY ri.name,ri.id`, v.OrganizationID)
	if err != nil {
		return nil, err
	}
	out := []AudiobookIntegrationSummary{}
	for rows.Next() {
		var in AudiobookIntegration
		if err = rows.Scan(&in.ID, &in.Name, &in.InstallationID, &in.CapabilityID, &in.OwnerID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, AudiobookIntegrationSummary{ID: in.ID, Name: in.Name, InstallationID: in.InstallationID, CapabilityID: in.CapabilityID, DownloadRoots: []string{}})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		var roots []string
		err = tx.QueryRow(ctx, `SELECT media_folder_id,download_roots,revision FROM bloem_audiobook_request_integrations WHERE integration_id=$1 AND organization_id=$2`, out[i].ID, v.OrganizationID).Scan(&out[i].LibraryID, &roots, &out[i].BindingRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[i].DownloadRoots = roots
		library, err := loadAudiobookLibrary(ctx, tx, v.OrganizationID, out[i].LibraryID)
		if errors.Is(err, ErrInvalid) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[i].LibraryName = library.Name
		out[i].Ready = validDownloadRoots(roots, library.Paths) == nil
	}
	return out, nil
}

// AudiobookLibraries lists audiobook libraries the organization may bind.
func (s *Service) AudiobookLibraries(ctx context.Context, v Viewer) ([]AudiobookLibrary, error) {
	if !v.Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = checkAudiobookAuthority(ctx, tx, v); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT f.id FROM media_folders f JOIN resource_owners ro ON ro.id=f.owner_id WHERE f.enabled AND lower(btrim(f.type)) IN ('audiobook','audiobooks') AND `+audiobookLibraryOwnership+` ORDER BY f.name,f.id LIMIT 200`, v.OrganizationID)
	if err != nil {
		return nil, err
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []AudiobookLibrary{}
	for _, id := range ids {
		library, err := loadAudiobookLibrary(ctx, tx, v.OrganizationID, id)
		if errors.Is(err, ErrInvalid) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, library)
	}
	return out, nil
}

// SetAudiobookIntegrationLibrary binds an integration to a library and its
// dedicated download folders. Platform-admin authority is checked by the
// caller. In-flight dispatches keep their frozen roots.
func (s *Service) SetAudiobookIntegrationLibrary(ctx context.Context, v Viewer, id string, libraryID int, roots []string) (AudiobookIntegrationSummary, error) {
	if !v.Admin {
		return AudiobookIntegrationSummary{}, ErrForbidden
	}
	if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n\t ") || libraryID < 0 {
		return AudiobookIntegrationSummary{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AudiobookIntegrationSummary{}, err
	}
	defer tx.Rollback(ctx)
	if err = checkAudiobookAuthority(ctx, tx, v); err != nil {
		return AudiobookIntegrationSummary{}, err
	}
	in, err := loadAudiobookIdentity(ctx, tx, v.OrganizationID, id)
	if errors.Is(err, ErrConflict) {
		return AudiobookIntegrationSummary{}, ErrInvalid
	}
	if err != nil {
		return AudiobookIntegrationSummary{}, err
	}
	summary := AudiobookIntegrationSummary{ID: in.ID, Name: in.Name, InstallationID: in.InstallationID, CapabilityID: in.CapabilityID, DownloadRoots: []string{}}
	if libraryID == 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM bloem_audiobook_request_integrations WHERE integration_id=$1 AND organization_id=$2`, id, v.OrganizationID); err != nil {
			return summary, err
		}
		return summary, tx.Commit(ctx)
	}
	library, err := loadAudiobookLibrary(ctx, tx, v.OrganizationID, libraryID)
	if err != nil {
		return summary, err
	}
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.ContainsFunc(root, unicode.IsControl) {
			return summary, ErrInvalid
		}
		cleaned = append(cleaned, strings.TrimSpace(root))
	}
	if err = validDownloadRoots(cleaned, library.Paths); err != nil {
		return summary, err
	}
	for _, root := range cleaned {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL AND file_path ~>=~ $2 AND file_path ~<~ $3 LIMIT $4) n`, libraryID, root+"/", root+"0", audiobookMaxRootFiles+1).Scan(&count); err != nil {
			return summary, err
		}
		if count > audiobookMaxRootFiles {
			return summary, fmt.Errorf("%w: %s is not a dedicated download folder (it already holds more than %d files)", ErrInvalid, root, audiobookMaxRootFiles)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_audiobook_request_integrations(integration_id,organization_id,media_folder_id,download_roots) VALUES($1,$2,$3,$4)
 ON CONFLICT(integration_id) DO UPDATE SET media_folder_id=EXCLUDED.media_folder_id,download_roots=EXCLUDED.download_roots,revision=bloem_audiobook_request_integrations.revision+1,updated_at=now()
 WHERE bloem_audiobook_request_integrations.organization_id=EXCLUDED.organization_id
 AND (bloem_audiobook_request_integrations.media_folder_id,bloem_audiobook_request_integrations.download_roots) IS DISTINCT FROM (EXCLUDED.media_folder_id,EXCLUDED.download_roots)`, id, v.OrganizationID, libraryID, cleaned)
	if err != nil {
		return summary, err
	}
	// A binding owned by another organization is never overwritten.
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT revision,organization_id=$2 FROM bloem_audiobook_request_integrations WHERE integration_id=$1`, id, v.OrganizationID).Scan(&summary.BindingRevision, &owned); err != nil {
		return summary, err
	}
	if !owned {
		return summary, ErrInvalid
	}
	summary.LibraryID, summary.LibraryName, summary.DownloadRoots, summary.Ready = libraryID, library.Name, slices.Clone(cleaned), true
	return summary, tx.Commit(ctx)
}
