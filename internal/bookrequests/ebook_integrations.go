package bookrequests

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const ebookIntegrationSQL = `
 SELECT ri.id,ri.name,pi.id,ri.capability_id,
 md5(jsonb_build_array(ri.base_url,ri.api_key_ref,ri.plugin_config,ri.updated_at,pi.version,pi.updated_at,pc.metadata,pc.updated_at,
   (SELECT jsonb_agg(jsonb_build_array(rc.config_key,rc.config_value) ORDER BY rc.config_key) FROM plugin_runtime_configs rc WHERE rc.plugin_installation_id=pi.id))::text)
 FROM request_integrations ri
 JOIN plugin_installations pi ON pi.id=ri.installation_id AND pi.enabled AND pi.kind='plugin' AND pi.plugin_id='bloem.requests.bookwarehouse'
 JOIN plugin_capabilities pc ON pc.plugin_installation_id=pi.id AND pc.capability_type='request_router.v1' AND pc.capability_id=ri.capability_id
 JOIN resource_owners ro ON ro.id=pi.owner_id
 JOIN organizations o ON o.id=$1 AND o.is_default AND (o.status='active' OR o.status='initializing')
 WHERE ri.enabled AND 'book'=ANY(ri.supported_media_types) AND ri.base_url<>'' AND ri.api_key_ref<>''
 AND ((ro.kind='organization' AND ro.organization_id=o.id) OR ro.kind='platform')`

func (s *Service) loadEbookIntegration(ctx context.Context, tx pgx.Tx, org uuid.UUID, id string) (EbookIntegration, error) {
	in := EbookIntegration{OrganizationID: org}
	err := tx.QueryRow(ctx, ebookIntegrationSQL+` AND ri.id=$2 FOR SHARE OF ri,pi,pc,ro,o`, org, id).Scan(&in.ID, &in.Name, &in.InstallationID, &in.CapabilityID, &in.ConfigurationRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrConflict
	}
	if err != nil {
		return in, err
	}
	err = tx.QueryRow(ctx, `SELECT media_folder_id,revision FROM bloem_ebook_request_integrations WHERE integration_id=$1 AND organization_id=$2 FOR SHARE`, id, org).Scan(&in.LibraryID, &in.BindingRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, nil
	}
	if err != nil {
		return in, err
	}
	_, source, err := s.loadEbookLibrary(ctx, tx, org, in.LibraryID)
	if err != nil {
		return in, err
	}
	in.StorageSourceKey, in.StorageConfigurationRevision = source.Key, source.ConfigurationRevision
	return in, nil
}

func (s *Service) loadEbookLibrary(ctx context.Context, tx pgx.Tx, org uuid.UUID, id int) (EbookLibrary, storagesource.SourceConfig, error) {
	library := EbookLibrary{ID: id}
	var location storagesource.Location
	var source storagesource.SourceConfig
	err := tx.QueryRow(ctx, `
 SELECT f.name,l.id,l.source_key,l.folder_id,s.key,s.owner_id,s.installation_id,s.plugin_id,s.provider_source_id,s.root_entry_id,s.configuration_revision,s.enabled
 FROM media_folders f JOIN library_storage_locations l ON l.folder_id=f.id
 JOIN bloem_storage_sources s ON s.key=l.source_key AND s.enabled AND s.plugin_id='bloem.storage.bookwarehouse'
 JOIN resource_owners ro ON ro.id=f.owner_id
 WHERE f.id=$2 AND f.enabled AND lower(btrim(f.type)) IN ('book','books','ebook','ebooks')
 AND ((ro.kind='organization' AND ro.organization_id=$1)
 OR (ro.kind='platform' AND EXISTS(SELECT 1 FROM organization_entitlements e WHERE e.organization_id=$1 AND e.media_folder_id=f.id AND e.root_owner_id=ro.id AND e.status='active' FOR SHARE)))`, org, id).Scan(&library.Name, &location.ID, &location.SourceKey, &location.FolderID, &source.Key, &source.OwnerID, &source.InstallationID, &source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return library, source, ErrInvalid
	}
	if err != nil {
		return library, source, err
	}
	if err := resourcetenancy.NewStore(s.pool).RequireStorageScanTx(ctx, tx, location, source); err != nil {
		if errors.Is(err, resourcetenancy.ErrResourceHidden) {
			return library, source, ErrInvalid
		}
		return library, source, err
	}
	return library, source, nil
}

func (s *Service) GetEbookIntegrationLibraries(ctx context.Context, v Viewer) ([]EbookIntegration, error) {
	if !v.Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, ebookIntegrationSQL+` ORDER BY ri.name,ri.id`, v.OrganizationID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id, name, capability, revision string
		var installation int
		if err = rows.Scan(&id, &name, &installation, &capability, &revision); err != nil {
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
	out := []EbookIntegration{}
	for _, id := range ids {
		in, err := s.loadEbookIntegration(ctx, tx, v.OrganizationID, id)
		if errors.Is(err, ErrInvalid) {
			out = append(out, in)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

func (s *Service) SetEbookIntegrationLibrary(ctx context.Context, v Viewer, id string, libraryID int) (EbookIntegration, error) {
	if !v.Admin {
		return EbookIntegration{}, ErrForbidden
	}
	if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n\t ") || libraryID < 0 {
		return EbookIntegration{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EbookIntegration{}, err
	}
	defer tx.Rollback(ctx)
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return EbookIntegration{}, err
	}
	// Validate the live connection without trusting a previous invalid binding.
	var installation int
	if err = tx.QueryRow(ctx, ebookIntegrationSQL+` AND ri.id=$2 FOR SHARE OF ri,pi,pc,ro,o`, v.OrganizationID, id).Scan(new(string), new(string), &installation, new(string), new(string)); errors.Is(err, pgx.ErrNoRows) {
		return EbookIntegration{}, ErrInvalid
	} else if err != nil {
		return EbookIntegration{}, err
	}
	if libraryID == 0 {
		_, err = tx.Exec(ctx, `DELETE FROM bloem_ebook_request_integrations WHERE integration_id=$1 AND organization_id=$2`, id, v.OrganizationID)
	} else {
		if _, _, err = s.loadEbookLibrary(ctx, tx, v.OrganizationID, libraryID); err != nil {
			return EbookIntegration{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_ebook_request_integrations(integration_id,organization_id,media_folder_id) VALUES($1,$2,$3) ON CONFLICT(integration_id) DO UPDATE SET media_folder_id=EXCLUDED.media_folder_id,revision=bloem_ebook_request_integrations.revision+1,updated_at=now() WHERE bloem_ebook_request_integrations.organization_id=EXCLUDED.organization_id AND bloem_ebook_request_integrations.media_folder_id IS DISTINCT FROM EXCLUDED.media_folder_id`, id, v.OrganizationID, libraryID)
	}
	if err != nil {
		return EbookIntegration{}, err
	}
	in, err := s.loadEbookIntegration(ctx, tx, v.OrganizationID, id)
	if err != nil {
		return in, err
	}
	if err = tx.Commit(ctx); err != nil {
		return in, err
	}
	return in, nil
}

func (s *Service) ValidateEbookIntegration(ctx context.Context, in EbookIntegration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = ebookAcquisitionEnabled(ctx, tx); err != nil {
		return err
	}
	current, err := s.loadEbookIntegration(ctx, tx, in.OrganizationID, in.ID)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if current != in || current.LibraryID == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Service) EbookLibraries(ctx context.Context, v Viewer) ([]EbookLibrary, error) {
	if !v.Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT f.id FROM media_folders f JOIN library_storage_locations l ON l.folder_id=f.id JOIN bloem_storage_sources s ON s.key=l.source_key WHERE f.enabled AND lower(btrim(f.type)) IN ('book','books','ebook','ebooks') AND s.enabled AND s.plugin_id='bloem.storage.bookwarehouse' ORDER BY f.name,f.id`)
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
	out := []EbookLibrary{}
	for _, id := range ids {
		library, _, err := s.loadEbookLibrary(ctx, tx, v.OrganizationID, id)
		if errors.Is(err, ErrInvalid) || errors.Is(err, resourcetenancy.ErrResourceHidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, library)
	}
	return out, nil
}

// New requests choose among current eligible bindings; existing dispatches
// retain their frozen provider identity and never move to a sibling backend.
func (s *Service) selectEbookIntegration(ctx context.Context, tx pgx.Tx, org uuid.UUID) (EbookIntegration, error) {
	rows, err := tx.Query(ctx, ebookIntegrationSQL+` AND EXISTS(SELECT 1 FROM bloem_ebook_request_integrations b WHERE b.integration_id=ri.id AND b.organization_id=$1) ORDER BY ri.id LIMIT 100`, org)
	if err != nil {
		return EbookIntegration{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id, new(string), new(int), new(string), new(string)); err != nil {
			rows.Close()
			return EbookIntegration{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return EbookIntegration{}, err
	}
	for _, id := range ids {
		in, err := s.loadEbookIntegration(ctx, tx, org, id)
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return in, err
		}
		if in.LibraryID > 0 {
			return in, nil
		}
	}
	return EbookIntegration{}, ErrConflict
}
