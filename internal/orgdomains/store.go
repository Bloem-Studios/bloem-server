package orgdomains

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Where a domain was registered from.
const (
	SourcePlatform = "platform"
	SourceGarden   = "garden"
)

var (
	ErrDomainTaken          = errors.New("orgdomains: the host is already registered")
	ErrDomainNotFound       = errors.New("orgdomains: the organization has no such domain")
	ErrOrganizationNotFound = errors.New("orgdomains: no such organization")
	ErrInvalidBrandingKey   = errors.New("orgdomains: branding keys start with branding.")
	ErrInvalidSource        = errors.New("orgdomains: a domain's source is platform or garden")
)

// Domain is one host registered to an organization.
type Domain struct {
	Host           string    `json:"host"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Primary        bool      `json:"primary"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
}

// Store reads and writes bloem_organization_domains and
// bloem_organization_branding.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// lockOrganization serializes domain writes for one organization, so two
// first domains cannot both become primary. It does not lock the
// organizations row, which Silo writes too.
func lockOrganization(ctx context.Context, tx pgx.Tx, org uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, org).Scan(&exists); err != nil {
		return fmt.Errorf("orgdomains: check organization: %w", err)
	}
	if !exists {
		return ErrOrganizationNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bloem_organization_domains:' || $1::text, 0))`, org); err != nil {
		return fmt.Errorf("orgdomains: lock organization domains: %w", err)
	}
	return nil
}

func (s *Store) inTransaction(ctx context.Context, org uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("orgdomains: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockOrganization(ctx, tx, org); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddDomain registers host to org. An organization's first domain is primary
// whatever primary says; primary on a later one moves the primary to it.
func (s *Store) AddDomain(ctx context.Context, org uuid.UUID, host, source string, primary bool) (Domain, error) {
	host, err := NormalizeHost(host)
	if err != nil {
		return Domain{}, err
	}
	if source != SourcePlatform && source != SourceGarden {
		return Domain{}, ErrInvalidSource
	}
	out := Domain{Host: host, OrganizationID: org, Source: source}
	err = s.inTransaction(ctx, org, func(tx pgx.Tx) error {
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM bloem_organization_domains WHERE organization_id = $1`, org).Scan(&others); err != nil {
			return fmt.Errorf("orgdomains: count domains: %w", err)
		}
		out.Primary = primary || others == 0
		if out.Primary {
			if _, err := tx.Exec(ctx, `UPDATE bloem_organization_domains SET is_primary = false WHERE organization_id = $1 AND is_primary`, org); err != nil {
				return fmt.Errorf("orgdomains: clear primary: %w", err)
			}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO bloem_organization_domains (host, organization_id, is_primary, source)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (host) DO NOTHING
			RETURNING created_at`, host, org, out.Primary, source).Scan(&out.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDomainTaken
		}
		if err != nil {
			return fmt.Errorf("orgdomains: add domain: %w", err)
		}
		return nil
	})
	if err != nil {
		return Domain{}, err
	}
	return out, nil
}

// SetPrimary makes host org's primary domain.
func (s *Store) SetPrimary(ctx context.Context, org uuid.UUID, host string) error {
	host, err := NormalizeHost(host)
	if err != nil {
		return ErrDomainNotFound
	}
	return s.inTransaction(ctx, org, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bloem_organization_domains WHERE host = $1 AND organization_id = $2)`, host, org).Scan(&exists); err != nil {
			return fmt.Errorf("orgdomains: find domain: %w", err)
		}
		if !exists {
			return ErrDomainNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE bloem_organization_domains SET is_primary = false WHERE organization_id = $1 AND is_primary`, org); err != nil {
			return fmt.Errorf("orgdomains: clear primary: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE bloem_organization_domains SET is_primary = true WHERE host = $1`, host); err != nil {
			return fmt.Errorf("orgdomains: set primary: %w", err)
		}
		return nil
	})
}

// RemoveDomain unregisters host from org. Removing the primary makes the
// oldest remaining domain primary.
func (s *Store) RemoveDomain(ctx context.Context, org uuid.UUID, host string) error {
	host, err := NormalizeHost(host)
	if err != nil {
		return ErrDomainNotFound
	}
	return s.inTransaction(ctx, org, func(tx pgx.Tx) error {
		var wasPrimary bool
		err := tx.QueryRow(ctx, `
			DELETE FROM bloem_organization_domains WHERE host = $1 AND organization_id = $2
			RETURNING is_primary`, host, org).Scan(&wasPrimary)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDomainNotFound
		}
		if err != nil {
			return fmt.Errorf("orgdomains: remove domain: %w", err)
		}
		if !wasPrimary {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bloem_organization_domains SET is_primary = true
			WHERE host = (SELECT host FROM bloem_organization_domains WHERE organization_id = $1 ORDER BY created_at, host LIMIT 1)`, org); err != nil {
			return fmt.Errorf("orgdomains: promote primary: %w", err)
		}
		return nil
	})
}

// ListDomains lists org's domains, the primary first, then by host.
func (s *Store) ListDomains(ctx context.Context, org uuid.UUID) ([]Domain, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT host, organization_id, is_primary, source, created_at
		FROM bloem_organization_domains WHERE organization_id = $1
		ORDER BY is_primary DESC, host`, org)
	if err != nil {
		return nil, fmt.Errorf("orgdomains: list domains: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Domain, error) {
		var d Domain
		err := row.Scan(&d.Host, &d.OrganizationID, &d.Primary, &d.Source, &d.CreatedAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("orgdomains: list domains: %w", err)
	}
	return out, nil
}

// SetBrandingValue stores org's own value for a branding.* setting; an empty
// value removes it, so the server's value applies again.
func (s *Store) SetBrandingValue(ctx context.Context, org uuid.UUID, key, value string) error {
	if !strings.HasPrefix(key, "branding.") {
		return ErrInvalidBrandingKey
	}
	if value == "" {
		if _, err := s.pool.Exec(ctx, `DELETE FROM bloem_organization_branding WHERE organization_id = $1 AND key = $2`, org, key); err != nil {
			return fmt.Errorf("orgdomains: clear branding value: %w", err)
		}
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO bloem_organization_branding (organization_id, key, value)
		SELECT id, $2, $3 FROM organizations WHERE id = $1
		ON CONFLICT (organization_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, org, key, value)
	if err != nil {
		return fmt.Errorf("orgdomains: set branding value: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrganizationNotFound
	}
	return nil
}

// BrandingValues are org's own branding.* values.
func (s *Store) BrandingValues(ctx context.Context, org uuid.UUID) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM bloem_organization_branding WHERE organization_id = $1`, org)
	if err != nil {
		return nil, fmt.Errorf("orgdomains: read branding values: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("orgdomains: read branding values: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

// AccountOrganization is the organization of the account's active membership.
// An account belongs to one organization (organization_memberships_one_per_account).
func (s *Store) AccountOrganization(ctx context.Context, accountID int) (uuid.UUID, bool, error) {
	var org uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT organization_id FROM organization_memberships
		WHERE account_id = $1 AND status = 'active'`, accountID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("orgdomains: account organization: %w", err)
	}
	return org, true, nil
}

// snapshot is every registered domain and organization branding value, as the
// directory serves them.
type snapshot struct {
	defaultOrg uuid.UUID
	hosts      map[string]uuid.UUID
	primary    map[uuid.UUID]string
	branding   map[uuid.UUID]map[string]string
}

func (s *Store) loadSnapshot(ctx context.Context) (snapshot, error) {
	out := snapshot{hosts: map[string]uuid.UUID{}, primary: map[uuid.UUID]string{}, branding: map[uuid.UUID]map[string]string{}}
	var def *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT public.bloem_default_organization_id()`).Scan(&def); err != nil {
		return snapshot{}, fmt.Errorf("orgdomains: default organization: %w", err)
	}
	if def != nil {
		out.defaultOrg = *def
	}
	rows, err := s.pool.Query(ctx, `SELECT host, organization_id, is_primary FROM bloem_organization_domains`)
	if err != nil {
		return snapshot{}, fmt.Errorf("orgdomains: load domains: %w", err)
	}
	for rows.Next() {
		var host string
		var org uuid.UUID
		var primary bool
		if err := rows.Scan(&host, &org, &primary); err != nil {
			rows.Close()
			return snapshot{}, fmt.Errorf("orgdomains: load domains: %w", err)
		}
		out.hosts[host] = org
		if primary {
			out.primary[org] = host
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshot{}, fmt.Errorf("orgdomains: load domains: %w", err)
	}
	rows, err = s.pool.Query(ctx, `SELECT organization_id, key, value FROM bloem_organization_branding`)
	if err != nil {
		return snapshot{}, fmt.Errorf("orgdomains: load branding: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var org uuid.UUID
		var key, value string
		if err := rows.Scan(&org, &key, &value); err != nil {
			return snapshot{}, fmt.Errorf("orgdomains: load branding: %w", err)
		}
		if out.branding[org] == nil {
			out.branding[org] = map[string]string{}
		}
		out.branding[org][key] = value
	}
	return out, rows.Err()
}
