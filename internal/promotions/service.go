package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// DefaultHomePosition is where the synthetic `promoted` home section lands
// when no active promotion carries a placement.home_position hint.
const DefaultHomePosition = 1

// Viewer identifies the profile a delivery is evaluated for. LibraryIDs nil
// means the profile's library access is unrestricted.
type Viewer struct {
	UserID     int
	ProfileID  string
	LibraryIDs []int
}

// Query selects cards for one surface. ContentID (detail / pre_playback)
// lets promotions with placement.content_ids restrict themselves to items.
type Query struct {
	Surface   string
	ContentID string
	Viewer    Viewer
}

// Service is the promotions store: admin CRUD plus per-profile delivery
// (window + organization scope + targeting + dismissals).
type Service struct {
	pool   *pgxpool.Pool
	clock  recipes.Clock
	stores userstore.UserStoreProvider
}

// NewService constructs the service. clock nil means the real clock (tests
// inject recipes.FixedClock); stores nil disables dismissal filtering.
func NewService(pool *pgxpool.Pool, clock recipes.Clock, stores userstore.UserStoreProvider) *Service {
	if clock == nil {
		clock = recipes.RealClock{}
	}
	return &Service{pool: pool, clock: clock, stores: stores}
}

// Now exposes the evaluation instant (the injected clock).
func (s *Service) Now() time.Time { return s.clock.Now().UTC() }

const promoColumns = `id, organization_id, surfaces, placement, kicker, headline, subtitle, image_url, image_width, image_height,
	deeplink, cta, priority, starts_at, ends_at, targeting, dismissible, created_by, created_at, updated_at`

func scanPromotion(row pgx.Row) (*Promotion, error) {
	var (
		p         Promotion
		placement []byte
		cta       []byte
		targeting []byte
		createdBy *int
	)
	if err := row.Scan(&p.ID, &p.OrganizationID, &p.Surfaces, &placement, &p.Kicker, &p.Headline, &p.Subtitle, &p.ImageURL,
		&p.ImageWidth, &p.ImageHeight, &p.Deeplink, &cta, &p.Priority, &p.StartsAt, &p.EndsAt, &targeting, &p.Dismissible,
		&createdBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if len(placement) > 0 {
		if err := json.Unmarshal(placement, &p.Placement); err != nil {
			return nil, fmt.Errorf("promotions: decode placement for %s: %w", p.ID, err)
		}
	}
	if len(cta) > 0 {
		var c CTA
		if err := json.Unmarshal(cta, &c); err != nil {
			return nil, fmt.Errorf("promotions: decode cta for %s: %w", p.ID, err)
		}
		p.CTA = &c
	}
	if len(targeting) > 0 {
		if err := json.Unmarshal(targeting, &p.Targeting); err != nil {
			return nil, fmt.Errorf("promotions: decode targeting for %s: %w", p.ID, err)
		}
	}
	if p.Targeting.Audience == "" {
		p.Targeting.Audience = notifications.AudienceAll
	}
	if createdBy != nil {
		p.CreatedBy = *createdBy
	}
	if p.Surfaces == nil {
		p.Surfaces = []string{}
	}
	p.StartsAt, p.EndsAt = p.StartsAt.UTC(), p.EndsAt.UTC()
	return &p, nil
}

func (s *Service) queryPromotions(ctx context.Context, sql string, args ...any) ([]Promotion, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Promotion, 0, 8)
	for rows.Next() {
		p, err := scanPromotion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func encode(n Normalized) (placement, cta, targeting []byte) {
	placement, _ = json.Marshal(n.Placement)
	if n.CTA != nil {
		cta, _ = json.Marshal(n.CTA)
	}
	targeting, _ = json.Marshal(n.Targeting)
	return placement, cta, targeting
}

// Create validates and stores a promotion authored by createdBy.
func (s *Service) Create(ctx context.Context, createdBy int, in Input) (*Promotion, error) {
	n, err := Normalize(in)
	if err != nil {
		return nil, err
	}
	placement, cta, targeting := encode(n)
	row := s.pool.QueryRow(ctx, `
		INSERT INTO promotions (id, organization_id, surfaces, placement, kicker, headline, subtitle, image_url, image_width, image_height,
			deeplink, cta, priority, starts_at, ends_at, targeting, dismissible, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING `+promoColumns,
		ulid.Make().String(), n.OrganizationID, n.Surfaces, placement, n.Kicker, n.Headline, n.Subtitle, n.ImageURL, n.ImageWidth, n.ImageHeight,
		n.Deeplink, cta, n.Priority, n.StartsAt, n.EndsAt, targeting, n.Dismissible, createdBy)
	p, err := scanPromotion(row)
	if err != nil {
		return nil, wrapWriteError("create", err)
	}
	return p, nil
}

// Update replaces every editable field (full-body PUT semantics).
func (s *Service) Update(ctx context.Context, id string, in Input) (*Promotion, error) {
	n, err := Normalize(in)
	if err != nil {
		return nil, err
	}
	placement, cta, targeting := encode(n)
	row := s.pool.QueryRow(ctx, `
		UPDATE promotions
		SET organization_id = $2, surfaces = $3, placement = $4, kicker = $5, headline = $6, subtitle = $7, image_url = $8,
			image_width = $9, image_height = $10, deeplink = $11, cta = $12, priority = $13, starts_at = $14, ends_at = $15,
			targeting = $16, dismissible = $17, updated_at = now()
		WHERE id = $1
		RETURNING `+promoColumns,
		id, n.OrganizationID, n.Surfaces, placement, n.Kicker, n.Headline, n.Subtitle, n.ImageURL, n.ImageWidth, n.ImageHeight,
		n.Deeplink, cta, n.Priority, n.StartsAt, n.EndsAt, targeting, n.Dismissible)
	p, err := scanPromotion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, wrapWriteError("update", err)
	}
	return p, nil
}

// Delete removes a promotion; unknown ids return ErrNotFound.
func (s *Service) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM promotions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("promotions: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one promotion by id.
func (s *Service) Get(ctx context.Context, id string) (*Promotion, error) {
	p, err := scanPromotion(s.pool.QueryRow(ctx, `SELECT `+promoColumns+` FROM promotions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("promotions: get: %w", err)
	}
	return p, nil
}

// List returns every promotion (admin view): highest priority first, then
// soonest window.
func (s *Service) List(ctx context.Context) ([]Promotion, error) {
	return s.queryPromotions(ctx, `SELECT `+promoColumns+` FROM promotions ORDER BY priority DESC, starts_at, id`)
}

// Active returns the cards to deliver for q: promotions whose window
// contains now, that list the surface, are deployment-wide or belong to one
// of the viewer's active organizations, whose targeting matches the viewer,
// that are not restricted to other content ids, and that the profile has
// not dismissed. Highest priority first.
func (s *Service) Active(ctx context.Context, q Query) ([]Card, error) {
	promos, err := s.activePromotions(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, len(promos))
	for _, p := range promos {
		out = append(out, p.Card())
	}
	return out, nil
}

// ActiveHome returns the home cards plus the layout position of the
// synthetic `promoted` section (the first card's placement.home_position, or
// DefaultHomePosition).
func (s *Service) ActiveHome(ctx context.Context, viewer Viewer) ([]Card, int, error) {
	promos, err := s.activePromotions(ctx, Query{Surface: SurfaceHome, Viewer: viewer})
	if err != nil {
		return nil, DefaultHomePosition, err
	}
	position := DefaultHomePosition
	out := make([]Card, 0, len(promos))
	for i, p := range promos {
		if i == 0 && p.Placement.HomePosition != nil {
			position = *p.Placement.HomePosition
		}
		out = append(out, p.Card())
	}
	return out, position, nil
}

func (s *Service) activePromotions(ctx context.Context, q Query) ([]Promotion, error) {
	if !IsSurface(q.Surface) {
		return nil, invalid("surface must be one of home, detail, pre_playback")
	}
	promos, err := s.queryPromotions(ctx, `
		SELECT `+promoColumns+` FROM promotions
		WHERE $1 = ANY(surfaces) AND starts_at <= $2 AND $2 < ends_at
		  AND (organization_id IS NULL OR organization_id IN (
		        SELECT m.organization_id
		        FROM organization_memberships m
		        JOIN organizations o ON o.id = m.organization_id
		        WHERE m.account_id = $3 AND m.status = 'active' AND o.status = 'active'))
		ORDER BY priority DESC, starts_at, id`, q.Surface, s.Now(), q.Viewer.UserID)
	if err != nil {
		return nil, fmt.Errorf("promotions: active: %w", err)
	}
	if len(promos) == 0 {
		return nil, nil
	}

	role, roleErr := s.viewerRole(ctx, q.Viewer.UserID)
	var orgs map[string]bool
	var orgsErr error
	dismissed := s.dismissed(ctx, q.Viewer, q.Surface)

	kept := promos[:0]
	for _, p := range promos {
		if _, ok := dismissed[p.ID]; ok {
			continue
		}
		if !contentAllowed(p.Placement, q.ContentID) {
			continue
		}
		switch p.Targeting.Audience {
		case notifications.AudienceRole:
			if roleErr != nil {
				return nil, roleErr
			}
		case notifications.AudienceOrganization:
			if orgs == nil && orgsErr == nil {
				orgs, orgsErr = s.viewerOrganizations(ctx, q.Viewer.UserID)
			}
			if orgsErr != nil {
				return nil, orgsErr
			}
		}
		if !Matches(p.Targeting, q.Viewer, role, orgs) {
			continue
		}
		kept = append(kept, p)
	}
	return kept, nil
}

// Matches evaluates S-1 targeting against a viewer with the given account
// role and active organization memberships (string UUIDs).
func Matches(t Targeting, v Viewer, role string, orgs map[string]bool) bool {
	switch t.Audience {
	case "", notifications.AudienceAll:
		return true
	case notifications.AudienceRole:
		return role != "" && t.Role == role
	case notifications.AudienceOrganization:
		return orgs[t.OrganizationID]
	case notifications.AudienceLibrary:
		if v.LibraryIDs == nil {
			return true
		}
		for _, id := range v.LibraryIDs {
			if id == t.LibraryID {
				return true
			}
		}
		return false
	case notifications.AudienceExplicit:
		for _, id := range t.UserIDs {
			if id == v.UserID {
				return true
			}
		}
		for _, id := range t.ProfileIDs {
			if id == v.ProfileID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *Service) viewerOrganizations(ctx context.Context, userID int) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.organization_id::text
		FROM organization_memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.account_id = $1 AND m.status = 'active' AND o.status = 'active'`, userID)
	if err != nil {
		return nil, fmt.Errorf("promotions: viewer organizations: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("promotions: viewer organizations: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

func contentAllowed(p Placement, contentID string) bool {
	if len(p.ContentIDs) == 0 {
		return true
	}
	if contentID == "" {
		return false
	}
	for _, id := range p.ContentIDs {
		if id == contentID {
			return true
		}
	}
	return false
}

func (s *Service) viewerRole(ctx context.Context, userID int) (string, error) {
	var role *string
	err := s.pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("promotions: viewer role: %w", err)
	}
	if role == nil {
		return "", nil
	}
	return *role, nil
}

// dismissed lists the promotion ids the profile dismissed on the surface.
// Store failures degrade to "nothing dismissed" (logged), never to an error.
func (s *Service) dismissed(ctx context.Context, v Viewer, surface string) map[string]struct{} {
	out := map[string]struct{}{}
	if s.stores == nil || v.UserID <= 0 || v.ProfileID == "" {
		return out
	}
	store, err := s.stores.ForUser(ctx, v.UserID)
	if err != nil || store == nil {
		slog.ErrorContext(ctx, "promotions: user store", "component", "promotions", "error", err)
		return out
	}
	rows, err := store.ListHomeDismissals(ctx, v.ProfileID, DismissalSurface(surface))
	if err != nil {
		slog.ErrorContext(ctx, "promotions: listing dismissals", "component", "promotions", "error", err)
		return out
	}
	for _, d := range rows {
		out[d.MediaItemID] = struct{}{}
	}
	return out
}

// wrapWriteError maps a foreign-key violation on organization_id to a
// validation error, everything else to an internal error.
func wrapWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return invalid("organization_id does not exist")
	}
	return fmt.Errorf("promotions: %s: %w", op, err)
}
