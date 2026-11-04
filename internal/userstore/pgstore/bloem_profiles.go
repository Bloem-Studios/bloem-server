package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// CreateProfileInTransaction inserts a profile using a caller-owned
// transaction. Account lifecycle creation uses it to bind the generated
// profile to the same receipt as the account and membership.
func (s *PostgresUserStore) CreateProfileInTransaction(ctx context.Context, tx pgx.Tx, p userstore.Profile) error {
	return createProfile(ctx, tx, s.userID, p)
}

// GetProfileInTransaction reads a profile through a caller-owned transaction.
func (s *PostgresUserStore) GetProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) (*userstore.Profile, error) {
	return getProfile(ctx, tx, s.userID, id)
}

// ListProfilesInTransaction lists profiles through a caller-owned transaction.
func (s *PostgresUserStore) ListProfilesInTransaction(ctx context.Context, tx pgx.Tx) ([]userstore.Profile, error) {
	profiles, _, err := bloemListProfiles(ctx, tx, s.userID)
	return profiles, err
}

// DeleteProfileInTransaction deletes a profile through a caller-owned transaction.
func (s *PostgresUserStore) DeleteProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) error {
	return deleteProfile(ctx, tx, s.userID, id, nil)
}

// resolveProfileTenancy stamps a new profile with its organization and access
// group: a profile written while its account is created joins the creation
// organization in the account's group; otherwise a legacy caller (no
// organization) resolves the account's legacy identity, an explicit
// organization must be an active, visible membership, and a profile without a
// group joins the organization's default group.
func resolveProfileTenancy(ctx context.Context, exec preferenceSettingsExecutor, userID int, p *userstore.Profile) error {
	if organizationID, ok := tenancy.CreationOrganization(ctx); ok && p.OrganizationID == "" {
		p.OrganizationID = organizationID.String()
		if p.AccessGroupID == nil {
			if err := exec.QueryRow(ctx, `SELECT access_group_id FROM users WHERE id = $1`, userID).Scan(&p.AccessGroupID); err != nil {
				return fmt.Errorf("resolving account group for profile %s: %w", p.ID, err)
			}
		}
	}
	if p.OrganizationID == "" {
		organizationID, legacyGroupID, err := tenancy.NewProfileIdentityResolver(exec).ResolveLegacyProfileIdentity(ctx, userID)
		if err != nil {
			return fmt.Errorf("resolving legacy identity for profile %s: %w", p.ID, err)
		}
		p.OrganizationID = organizationID.String()
		if p.AccessGroupID == nil {
			p.AccessGroupID = legacyGroupID
		}
	} else {
		var activeMembership bool
		if err := exec.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM organization_memberships memberships
				JOIN organizations ON organizations.id = memberships.organization_id
				WHERE memberships.account_id = $1
				  AND memberships.organization_id = $2
				  AND memberships.status = 'active'
				  AND organizations.status <> 'suspended'
			)`, userID, p.OrganizationID).Scan(&activeMembership); err != nil {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, err)
		}
		if !activeMembership {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
		}
	}
	if p.AccessGroupID == nil {
		var defaultGroupID int64
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM access_groups
			WHERE organization_id = $1
			  AND is_default`, p.OrganizationID).Scan(&defaultGroupID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
			}
			return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, err)
		}
		p.AccessGroupID = &defaultGroupID
	}
	return nil
}

// bloemProfileColumns are the user_profiles columns Bloem adds: a profile's own
// sign-in, its organization and its access group. Bloem's profile reads select
// them after Silo's own columns and scan them through scanProfile's extra
// targets, so Silo's column list and scan stay as Silo wrote them.
const bloemProfileColumns = `COALESCE(login_email, ''), credential_revision, organization_id::text, access_group_id`

// bloemProfileByIDQuery is Silo's profileByIDQuery with bloemProfileColumns
// after its last column.
var bloemProfileByIDQuery = bloemWithProfileColumns(profileByIDQuery)

// bloemWithProfileColumns adds bloemProfileColumns to the end of a Silo profile
// query's select list. It panics when the query no longer reads FROM
// user_profiles on its own line, so a reshaped Silo query fails every test.
func bloemWithProfileColumns(query string) string {
	const from = "\n\tFROM user_profiles "
	at := strings.LastIndex(query, from)
	if at < 0 {
		panic("pgstore: Silo's profile query no longer selects FROM user_profiles")
	}
	return query[:at] + ",\n\t       " + bloemProfileColumns + query[at:]
}

// bloemProfileFields holds bloemProfileColumns while scanProfile builds the
// profile.
type bloemProfileFields struct {
	loginEmail         string
	credentialRevision int64
	organizationID     string
	accessGroupID      *int64
}

func (f *bloemProfileFields) targets() []any {
	return []any{&f.loginEmail, &f.credentialRevision, &f.organizationID, &f.accessGroupID}
}

func (f *bloemProfileFields) apply(p *userstore.Profile) {
	p.LoginEmail = f.loginEmail
	p.CredentialRevision = f.credentialRevision
	p.OrganizationID = f.organizationID
	p.AccessGroupID = f.accessGroupID
}

// bloemGetProfile is getProfile on Bloem's schema: Silo's read of the profile
// and its allowed libraries, plus bloemProfileColumns. It always handles the
// read; getProfile keeps Silo's own read below its hook.
func bloemGetProfile(ctx context.Context, db preferenceSettingsExecutor, userID int, id string) (*userstore.Profile, bool, error) {
	p, err := bloemProfileFromRow(db.QueryRow(ctx, bloemProfileByIDQuery, userID, id), id)
	return p, true, err
}

// bloemQueueProfile is QueueProfile on Bloem's schema, with bloemGetProfile's
// read and answer. It always handles the read; QueueProfile keeps Silo's own
// below its hook.
func bloemQueueProfile(batch *pgx.Batch, userID int, id string) (func(pgx.BatchResults) (*userstore.Profile, error), bool) {
	batch.Queue(bloemProfileByIDQuery, userID, id)
	return func(results pgx.BatchResults) (*userstore.Profile, error) {
		return bloemProfileFromRow(results.QueryRow(), id)
	}, true
}

// bloemProfileFromRow is Silo's profileFromRow for bloemProfileByIDQuery.
func bloemProfileFromRow(row pgx.Row, id string) (*userstore.Profile, error) {
	var libraryIDs []int
	var fields bloemProfileFields
	p, err := scanProfile(row, append([]any{&libraryIDs}, fields.targets()...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying profile %s: %w", id, err)
	}
	fields.apply(p)
	// A profile without allowed libraries keeps a nil list, as ListProfiles
	// reports it.
	if len(libraryIDs) > 0 {
		p.AllowedLibraryIDs = libraryIDs
	}
	return p, nil
}

// bloemListProfiles is ListProfiles on Bloem's schema and in a caller's
// transaction: Silo's columns plus bloemProfileColumns, in creation order with
// the id as tie-breaker. It always handles the read; ListProfiles keeps Silo's
// own read below its hook.
func bloemListProfiles(ctx context.Context, exec preferenceSettingsExecutor, userID int) ([]userstore.Profile, bool, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, name, avatar, pin_hash, is_child, is_primary, max_content_rating, COALESCE(max_advisory_age, 0),
		       require_advisory_age,
		       quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
		       auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview, library_restrictions_enabled,
		       show_forced_subtitles, max_playback_quality, pin_revision, created_at, updated_at,
		       `+bloemProfileColumns+`
		FROM user_profiles WHERE user_id = $1 ORDER BY created_at ASC, id ASC`, userID)
	if err != nil {
		return nil, true, fmt.Errorf("listing profiles: %w", err)
	}
	defer rows.Close()

	var profiles []userstore.Profile
	for rows.Next() {
		var fields bloemProfileFields
		p, err := scanProfile(rows, fields.targets()...)
		if err != nil {
			return nil, true, fmt.Errorf("scanning profile row: %w", err)
		}
		fields.apply(p)
		profiles = append(profiles, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("iterating profile rows: %w", err)
	}
	if err := bloemAttachAllowedLibraries(ctx, exec, userID, profiles); err != nil {
		return nil, true, err
	}
	return profiles, true, nil
}

// bloemAttachAllowedLibraries is Silo's attachAllowedLibraries for a caller's
// executor, such as a transaction, instead of the store's pool.
func bloemAttachAllowedLibraries(ctx context.Context, exec preferenceSettingsExecutor, userID int, profiles []userstore.Profile) error {
	if len(profiles) == 0 {
		return nil
	}
	ids := make([]string, len(profiles))
	for i := range profiles {
		ids[i] = profiles[i].ID
	}
	rows, err := exec.Query(ctx,
		`SELECT profile_id, library_id
		FROM user_profile_allowed_libraries
		WHERE user_id = $1 AND profile_id = ANY($2)
		ORDER BY library_id ASC`,
		userID,
		ids,
	)
	if err != nil {
		return fmt.Errorf("listing allowed libraries for user %d: %w", userID, err)
	}
	defer rows.Close()

	var allowedLibraries []userstore.ProfileAllowedLibrary
	for rows.Next() {
		var allowedLibrary userstore.ProfileAllowedLibrary
		if err := rows.Scan(&allowedLibrary.ProfileID, &allowedLibrary.LibraryID); err != nil {
			return fmt.Errorf("scanning allowed library for user %d: %w", userID, err)
		}
		allowedLibraries = append(allowedLibraries, allowedLibrary)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating allowed libraries for user %d: %w", userID, err)
	}
	userstore.AttachAllowedLibraries(profiles, allowedLibraries)
	return nil
}
