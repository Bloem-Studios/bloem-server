package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnnouncementRepository owns notification_announcements.
type AnnouncementRepository struct {
	pool *pgxpool.Pool
}

// NewAnnouncementRepository creates an AnnouncementRepository.
func NewAnnouncementRepository(pool *pgxpool.Pool) *AnnouncementRepository {
	return &AnnouncementRepository{pool: pool}
}

const announcementSelect = `
	SELECT id, type, body, targeting, created_by, recipient_count, created_at, withdrawn_at
	FROM notification_announcements`

func scanAnnouncement(row pgx.Row) (Announcement, error) {
	var a Announcement
	var body, targeting []byte
	if err := row.Scan(&a.ID, &a.Type, &body, &targeting, &a.CreatedBy, &a.RecipientCount, &a.CreatedAt, &a.WithdrawnAt); err != nil {
		return Announcement{}, err
	}
	if err := json.Unmarshal(body, &a.Body); err != nil {
		return Announcement{}, fmt.Errorf("decode announcement body: %w", err)
	}
	if err := json.Unmarshal(targeting, &a.Targeting); err != nil {
		return Announcement{}, fmt.Errorf("decode announcement targeting: %w", err)
	}
	return a, nil
}

// Insert writes the announcement inside the fanout transaction so the
// deliveries' FK resolves and an aborted fanout leaves no orphan.
func (r *AnnouncementRepository) Insert(ctx context.Context, tx pgx.Tx, a Announcement) error {
	body, err := json.Marshal(a.Body)
	if err != nil {
		return fmt.Errorf("encode announcement body: %w", err)
	}
	targeting, err := json.Marshal(a.Targeting)
	if err != nil {
		return fmt.Errorf("encode announcement targeting: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO notification_announcements
			(id, type, body, targeting, created_by, recipient_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ID, a.Type, body, targeting, a.CreatedBy, a.RecipientCount, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert announcement: %w", err)
	}
	return nil
}

// List returns announcements newest-first, withdrawn ones included so the
// admin sees the audit trail.
func (r *AnnouncementRepository) List(ctx context.Context, limit int) ([]Announcement, error) {
	rows, err := r.pool.Query(ctx, announcementSelect+` ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list announcements: %w", err)
	}
	defer rows.Close()
	out := make([]Announcement, 0, 16)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get returns one announcement; (nil, nil) when absent.
func (r *AnnouncementRepository) Get(ctx context.Context, id string) (*Announcement, error) {
	a, err := scanAnnouncement(r.pool.QueryRow(ctx, announcementSelect+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get announcement: %w", err)
	}
	return &a, nil
}

// MarkWithdrawn stamps withdrawn_at once; reports whether the row existed.
func (r *AnnouncementRepository) MarkWithdrawn(ctx context.Context, tx pgx.Tx, id string, at time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE notification_announcements
		SET withdrawn_at = COALESCE(withdrawn_at, $2)
		WHERE id = $1`, id, at)
	if err != nil {
		return false, fmt.Errorf("mark announcement withdrawn: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
