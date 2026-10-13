package api

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type listCollectionDomain struct {
	managed *managedtracking.Service
	source  *managedCalendarSource
}

var _ listcollections.CollectionDomain = (*listCollectionDomain)(nil)

func (d *listCollectionDomain) nativeScopeTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject) (access.Scope, error) {
	if d == nil || d.source == nil || tx == nil || s.UserID < 1 || s.ProfileID == "" {
		return access.Scope{}, listcollections.ErrAuthority
	}
	var role string
	err := tx.QueryRow(ctx, `SELECT u.role FROM users u JOIN user_profiles p ON p.user_id=u.id JOIN organizations o ON o.id=p.organization_id JOIN organization_memberships m ON m.organization_id=o.id AND m.account_id=u.id WHERE u.id=$1 AND p.id=$2 AND u.enabled AND COALESCE(p.pin_hash,'')='' AND m.status='active' AND (o.status='active' OR (o.is_default AND o.status='initializing')) FOR SHARE OF u,p,o,m`, s.UserID, s.ProfileID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Scope{}, listcollections.ErrAuthority
	}
	if err != nil {
		return access.Scope{}, err
	}
	if s.IsAdmin != (role == "admin") {
		return access.Scope{}, listcollections.ErrAuthority
	}
	scope, err := d.source.scope(ctx, managedtracking.Profile{AccountID: strconv.Itoa(s.UserID), ProfileID: s.ProfileID, AccountActive: true, Active: true})
	if err != nil {
		return access.Scope{}, listcollections.ErrAuthority
	}
	return scope, nil
}

func (d *listCollectionDomain) AuthorizeTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject) error {
	if d == nil || d.managed == nil {
		return listcollections.ErrAuthority
	}
	var capable bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_capabilities WHERE plugin_installation_id=$1 AND capability_type='watch_sync_provider.v1' AND capability_id='pastime' AND metadata->'metadata' @> '{"managed_list_collections":true}'::jsonb)`, s.InstallationID).Scan(&capable); err != nil {
		return err
	}
	if !capable {
		return listcollections.ErrAuthority
	}
	if _, err := d.managed.AuthorizeListTx(ctx, tx, s.InstallationID, s.InstanceID, strconv.Itoa(s.UserID), s.ProfileID); err != nil {
		if errors.Is(err, managedtracking.ErrAuthority) {
			return listcollections.ErrAuthority
		}
		return err
	}
	_, err := d.nativeScopeTx(ctx, tx, s)
	return err
}

// Detachment checks local ownership and viewer policy without requiring a live
// provider. Source suspension must never strand a collection in imported mode.
func (d *listCollectionDomain) AuthorizeDetachTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject) error {
	_, err := d.nativeScopeTx(ctx, tx, s)
	return err
}

// Silo's acting administrator gate requires the account's primary profile.
// Keep this owned adapter on that same rule for server collection mutations.
func (d *listCollectionDomain) serverAuthorityTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject) error {
	if !s.IsAdmin {
		return listcollections.ErrAuthority
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1 AND p.id=$2 AND u.enabled AND u.role='admin' AND p.is_primary)`, s.UserID, s.ProfileID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return listcollections.ErrAuthority
	}
	return nil
}

func listSelectedLibraries(ctx context.Context, tx pgx.Tx, scope access.Scope, libraries []int) error {
	if len(libraries) == 0 {
		return listcollections.ErrAuthority
	}
	for _, id := range libraries {
		if id < 1 || scope.AllowedLibraryIDs != nil && !slices.Contains(scope.AllowedLibraryIDs, id) || slices.Contains(scope.DisabledLibraryIDs, id) {
			return listcollections.ErrAuthority
		}
	}
	rows, err := tx.Query(ctx, `SELECT id FROM media_folders WHERE id=ANY($1::int[]) AND enabled ORDER BY id FOR SHARE`, libraries)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return err
	}
	for _, id := range libraries {
		if !slices.Contains(ids, id) {
			return listcollections.ErrAuthority
		}
	}
	return nil
}

func (d *listCollectionDomain) CreateTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, in listcollections.CreateInput, binding string) (string, int64, error) {
	scope, err := d.nativeScopeTx(ctx, tx, s)
	if err != nil {
		return "", 0, err
	}
	switch in.Scope {
	case "personal":
		if len(in.LibraryIDs) != 0 {
			return "", 0, listcollections.ErrAuthority
		}
		provider := pgstore.NewBloemListCollectionCreator(d.source.pool, tx, s.UserID, binding)
		view, err := handlers.NewCollectionHandler(provider).CreatePersonalCollection(ctx, handlers.PersonalCollectionCreateCommand{UserID: s.UserID, ProfileID: s.ProfileID, Request: handlers.PersonalCollectionCreateRequest{Name: in.Name, CollectionType: "synced", IsShared: in.IsShared}})
		if err != nil {
			return "", 0, err
		}
		var revision int64
		err = tx.QueryRow(ctx, `SELECT revision FROM user_collection_revisions WHERE user_id=$1 AND collection_id=$2`, s.UserID, view.ID).Scan(&revision)
		return view.ID, revision, err
	case "server":
		if err = d.serverAuthorityTx(ctx, tx, s); err != nil {
			return "", 0, err
		}
		if !s.IsAdmin || in.IsShared {
			return "", 0, listcollections.ErrAuthority
		}
		if err = listSelectedLibraries(ctx, tx, scope, in.LibraryIDs); err != nil {
			return "", 0, err
		}
		return catalog.BloemCreateListCollectionTx(ctx, tx, in.Name, in.LibraryIDs, binding)
	default:
		return "", 0, listcollections.ErrSnapshot
	}
}

func (d *listCollectionDomain) CandidatesTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, b listcollections.Binding, entries []listcollections.Entry) ([]listcollections.Candidate, error) {
	scope, err := d.nativeScopeTx(ctx, tx, s)
	if err != nil {
		return nil, err
	}
	if b.Scope == "server" {
		if err = d.serverAuthorityTx(ctx, tx, s); err != nil {
			return nil, err
		}
		if !s.IsAdmin {
			return nil, listcollections.ErrAuthority
		}
		if err = listSelectedLibraries(ctx, tx, scope, b.LibraryIDs); err != nil {
			return nil, err
		}
		scope.AllowedLibraryIDs = slices.Clone(b.LibraryIDs)
	} else if b.Scope != "personal" {
		return nil, listcollections.ErrAuthority
	}
	return catalog.BloemListCandidatesTx(ctx, tx, catalog.BrowseFilters{UserID: s.UserID, ProfileID: s.ProfileID, LibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality}, entries)
}

func (d *listCollectionDomain) ReplaceTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, b listcollections.Binding, items []string) (int64, error) {
	scope, err := d.nativeScopeTx(ctx, tx, s)
	if err != nil {
		return 0, err
	}
	if b.Scope != "personal" && b.Scope != "server" || b.Scope == "server" && !s.IsAdmin {
		return 0, listcollections.ErrAuthority
	}
	if b.Scope == "server" {
		if err = d.serverAuthorityTx(ctx, tx, s); err != nil {
			return 0, err
		}
		if err = listSelectedLibraries(ctx, tx, scope, b.LibraryIDs); err != nil {
			return 0, err
		}
		scope.AllowedLibraryIDs = slices.Clone(b.LibraryIDs)
	}
	if err = catalog.BloemAuthorizeListItemsTx(ctx, tx, catalog.BrowseFilters{UserID: s.UserID, ProfileID: s.ProfileID, LibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality}, items); err != nil {
		return 0, err
	}
	if b.Scope == "personal" {
		return pgstore.BloemReplaceListCollectionItemsTx(ctx, tx, s.UserID, s.ProfileID, b.TargetID, b.ID, items)
	}
	return catalog.BloemReplaceListCollectionItemsTx(ctx, tx, b.TargetID, b.ID, items)
}

func (d *listCollectionDomain) DisconnectTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, b listcollections.Binding) error {
	if _, err := d.nativeScopeTx(ctx, tx, s); err != nil {
		return err
	}
	if b.Scope == "personal" {
		result, err := tx.Exec(ctx, `UPDATE user_personal_collections SET collection_type='manual',source_config=source_config-'bloem_list_binding' WHERE user_id=$1 AND creator_profile_id=$2 AND id=$3 AND native AND collection_type='synced' AND source_config->>'bloem_list_binding'=$4`, s.UserID, s.ProfileID, b.TargetID, b.ID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return listcollections.ErrAuthority
		}
		return nil
	}
	if b.Scope != "server" || !s.IsAdmin {
		return listcollections.ErrAuthority
	}
	if err := d.serverAuthorityTx(ctx, tx, s); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE library_collections SET source_config=source_config-'bloem_list_binding' WHERE id=$1 AND collection_type='manual' AND source_config->>'bloem_list_binding'=$2`, b.TargetID, b.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return listcollections.ErrAuthority
	}
	return nil
}
