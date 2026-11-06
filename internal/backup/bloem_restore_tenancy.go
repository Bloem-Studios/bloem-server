package backup

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Bloem: every account belongs to one organization (its membership), and
// every profile to its account's organization and an access group there.
// A restore set records both, so a selective restore puts an account back
// in the organization it was in at the backup, matched by id on this
// server, or in the default organization when that one is gone or the
// backup came from another server. Its access group and its profiles'
// groups are matched by name within that organization.
// docs/architecture/bloem-backup.md describes the rule.

// BloemOrganizationEntry is the organization an account belonged to when the
// backup was taken.
type BloemOrganizationEntry struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// bloemAttachOrganizations records each account's organization in its
// restore-set entry.
func bloemAttachOrganizations(ctx context.Context, tx pgx.Tx, accounts []AccountEntry) error {
	rows, err := tx.Query(ctx, `
		SELECT m.account_id, o.id::text, o.slug, o.name
		FROM organization_memberships m JOIN organizations o ON o.id = m.organization_id`)
	if err != nil {
		return fmt.Errorf("reading account organizations: %w", err)
	}
	defer rows.Close()
	byAccount := map[int]BloemOrganizationEntry{}
	for rows.Next() {
		var accountID int
		var o BloemOrganizationEntry
		if err := rows.Scan(&accountID, &o.ID, &o.Slug, &o.Name); err != nil {
			return err
		}
		byAccount[accountID] = o
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range accounts {
		if o, ok := byAccount[accounts[i].ID]; ok {
			accounts[i].BloemOrganization = &o
		}
	}
	return nil
}

// bloemAttachProfileGroups records each profile's access group name in its
// restore-set entry.
func bloemAttachProfileGroups(ctx context.Context, tx pgx.Tx, userID int, profiles []ProfileEntry) error {
	rows, err := tx.Query(ctx, `
		SELECT p.id, g.name FROM user_profiles p JOIN access_groups g ON g.id = p.access_group_id
		WHERE p.user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("reading profile access groups: %w", err)
	}
	defer rows.Close()
	groups := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		groups[id] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range profiles {
		profiles[i].BloemAccessGroup = groups[profiles[i].ID]
	}
	return nil
}

// bloemPlanContext names, as the creation organization (see
// tenancy.WithCreationOrganization), the organization a backup account is
// restored into: the one it was in at the backup when this server has it,
// else the default organization. The planner looks the account's access
// group up there, and a new account's membership is created there.
func bloemPlanContext(ctx context.Context, q queryer, account AccountEntry) (context.Context, error) {
	id, err := bloemRestoreOrganization(ctx, q, account)
	if err != nil {
		return ctx, err
	}
	return tenancy.WithCreationOrganization(ctx, id), nil
}

func bloemRestoreOrganization(ctx context.Context, q queryer, account AccountEntry) (uuid.UUID, error) {
	if id, ok, err := bloemBackupOrganization(ctx, q, account.BloemOrganization); err != nil || ok {
		return id, err
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errors.New("this server has no default organization to restore accounts into")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("finding the default organization: %w", err)
	}
	return id, nil
}

// bloemBackupOrganization reports the backup's organization when this server
// has one with its id.
func bloemBackupOrganization(ctx context.Context, q queryer, o *BloemOrganizationEntry) (uuid.UUID, bool, error) {
	if o == nil {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(o.ID)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("the backup names organization %q, which is not an id: %w", o.ID, err)
	}
	var found bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, id).Scan(&found); err != nil {
		return uuid.Nil, false, fmt.Errorf("finding organization %s: %w", o.ID, err)
	}
	return id, found, nil
}
