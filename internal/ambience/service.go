package ambience

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/Silo-Server/silo-server/internal/sections/recipes"
)

// Service is the ambience registry: pack CRUD, active-window evaluation
// against the sections seasonal clock, and artwork storage.
type Service struct {
	pool  *pgxpool.Pool
	clock recipes.Clock
	store AssetStore
}

// NewService constructs the registry. clock nil means the real clock (tests
// inject recipes.FixedClock); store nil disables artwork upload/serving
// (packs then reference external https URLs only). Pass a nil AssetStore,
// not a typed-nil client, when S3 is absent.
func NewService(pool *pgxpool.Pool, clock recipes.Clock, store AssetStore) *Service {
	if clock == nil {
		clock = recipes.RealClock{}
	}
	return &Service{pool: pool, clock: clock, store: store}
}

// Now exposes the evaluation instant (the injected seasonal clock).
func (s *Service) Now() time.Time { return s.clock.Now().UTC() }

const packColumns = `id, effect_id, starts_at, ends_at, intensity, surfaces, assets, organization_id, created_by, created_at, updated_at`

func scanPack(row pgx.Row) (*Pack, error) {
	var (
		p         Pack
		assets    []byte
		createdBy *int
		orgID     *uuid.UUID
	)
	if err := row.Scan(&p.ID, &p.EffectID, &p.Window.StartsAt, &p.Window.EndsAt, &p.Intensity, &p.Surfaces, &assets, &orgID, &createdBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		if err := json.Unmarshal(assets, &p.Assets); err != nil {
			return nil, fmt.Errorf("ambience: decode assets for %s: %w", p.ID, err)
		}
	}
	if createdBy != nil {
		p.CreatedBy = *createdBy
	}
	p.OrganizationID = orgID
	p.Window.StartsAt = p.Window.StartsAt.UTC()
	p.Window.EndsAt = p.Window.EndsAt.UTC()
	if p.Surfaces == nil {
		p.Surfaces = []string{}
	}
	return &p, nil
}

func (s *Service) queryPacks(ctx context.Context, sql string, args ...any) ([]Pack, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Pack, 0, 8)
	for rows.Next() {
		p, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Create validates and stores a pack authored by createdBy.
func (s *Service) Create(ctx context.Context, createdBy int, in Input) (*Pack, error) {
	n, err := Normalize(in)
	if err != nil {
		return nil, err
	}
	assets, _ := json.Marshal(n.Assets)
	row := s.pool.QueryRow(ctx, `
		INSERT INTO ambience_packs (id, effect_id, starts_at, ends_at, intensity, surfaces, assets, organization_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+packColumns,
		ulid.Make().String(), n.EffectID, n.Window.StartsAt, n.Window.EndsAt, n.Intensity, n.Surfaces, assets, n.OrganizationID, createdBy)
	p, err := scanPack(row)
	if err != nil {
		return nil, wrapWriteError("create", err)
	}
	return p, nil
}

// Update replaces every editable field of a pack (full-body PUT semantics).
func (s *Service) Update(ctx context.Context, id string, in Input) (*Pack, error) {
	n, err := Normalize(in)
	if err != nil {
		return nil, err
	}
	assets, _ := json.Marshal(n.Assets)
	row := s.pool.QueryRow(ctx, `
		UPDATE ambience_packs
		SET effect_id = $2, starts_at = $3, ends_at = $4, intensity = $5, surfaces = $6, assets = $7, organization_id = $8, updated_at = now()
		WHERE id = $1
		RETURNING `+packColumns,
		id, n.EffectID, n.Window.StartsAt, n.Window.EndsAt, n.Intensity, n.Surfaces, assets, n.OrganizationID)
	p, err := scanPack(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, wrapWriteError("update", err)
	}
	return p, nil
}

// Delete removes a pack; unknown ids return ErrNotFound. Stored artwork
// objects are left in place (same orphan policy as branding assets).
func (s *Service) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM ambience_packs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("ambience: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one pack by id.
func (s *Service) Get(ctx context.Context, id string) (*Pack, error) {
	p, err := scanPack(s.pool.QueryRow(ctx, `SELECT `+packColumns+` FROM ambience_packs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ambience: get: %w", err)
	}
	return p, nil
}

// List returns every pack (admin view), soonest window first.
func (s *Service) List(ctx context.Context) ([]Pack, error) {
	return s.queryPacks(ctx, `SELECT `+packColumns+` FROM ambience_packs ORDER BY starts_at, id`)
}

// ActivePublic returns the deployment-wide packs whose window contains the
// clock's now. This feeds the unauthenticated branding payload, so org-scoped
// packs are never included.
func (s *Service) ActivePublic(ctx context.Context) ([]Wire, error) {
	packs, err := s.queryPacks(ctx, `
		SELECT `+packColumns+` FROM ambience_packs
		WHERE organization_id IS NULL AND starts_at <= $1 AND $1 < ends_at
		ORDER BY starts_at, id`, s.Now())
	return wire(packs), err
}

// ActiveForAccount returns the active deployment-wide packs plus the active
// packs of every organization the account is an active member of.
func (s *Service) ActiveForAccount(ctx context.Context, accountID int) ([]Wire, error) {
	packs, err := s.queryPacks(ctx, `
		SELECT `+packColumns+` FROM ambience_packs
		WHERE starts_at <= $1 AND $1 < ends_at
		  AND (organization_id IS NULL OR organization_id IN (
		        SELECT m.organization_id
		        FROM organization_memberships m
		        JOIN organizations o ON o.id = m.organization_id
		        WHERE m.account_id = $2 AND m.status = 'active' AND o.status = 'active'))
		ORDER BY starts_at, id`, s.Now(), accountID)
	return wire(packs), err
}

// wrapWriteError maps a foreign-key violation on organization_id to a
// validation error (the admin named an unknown organization), everything
// else to an internal error.
func wrapWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return invalid("organization_id does not exist")
	}
	return fmt.Errorf("ambience: %s: %w", op, err)
}

func wire(packs []Pack) []Wire {
	out := make([]Wire, 0, len(packs))
	for _, p := range packs {
		out = append(out, p.Wire())
	}
	return out
}
