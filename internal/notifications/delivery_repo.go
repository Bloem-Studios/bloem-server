package notifications

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeliveryRepository owns notification_deliveries.
type DeliveryRepository struct {
	pool *pgxpool.Pool
}

// NewDeliveryRepository creates a DeliveryRepository.
func NewDeliveryRepository(pool *pgxpool.Pool) *DeliveryRepository {
	return &DeliveryRepository{pool: pool}
}

// Cursor is an opaque pagination cursor over (created_at, id).
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode returns the opaque wire form of the cursor.
func (c Cursor) Encode() string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses an opaque cursor produced by Encode.
func DecodeCursor(encoded string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Cursor{}, errors.New("invalid cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	return Cursor{CreatedAt: createdAt, ID: parts[1]}, nil
}

// deliveryRowSelect joins display metadata so clients can render a row
// without an extra lookup. LEFT JOINs keep operational delivery types (no
// episode/series) and deleted catalog rows renderable.
const deliveryRowSelect = `
	SELECT d.id, d.release_event_id, d.user_id, d.profile_id, d.library_id, d.series_id, d.episode_id,
	       d.type, d.reason_flags, d.status, d.read_at, d.delivered_at, d.created_at,
	       d.body, d.expires_at, d.dismissed_at, d.announcement_id,
	       COALESCE(s.title, '') AS series_title,
	       COALESCE(e.title, '') AS episode_title,
	       e.season_number, e.episode_number,
	       COALESCE(s.poster_path, '') AS poster_path,
	       COALESCE(s.poster_thumbhash, '') AS poster_thumbhash,
	       COALESCE(s.poster_source_path, '') AS poster_source_path,
	       COALESCE(s.type, '') AS media_type,
	       COALESCE(s.year, 0) AS year,
	       COALESCE(s.overview, '') AS series_overview,
	       COALESCE(e.overview, '') AS episode_overview,
	       COALESCE(s.genres, '{}'::text[]) AS genres,
	       COALESCE(s.content_rating, '') AS content_rating,
	       COALESCE(s.rating_imdb, 0) AS rating_imdb,
	       COALESCE(s.rating_tmdb, 0) AS rating_tmdb,
	       COALESCE(s.imdb_id, '') AS imdb_id,
	       COALESCE(s.tmdb_id, '') AS tmdb_id,
	       COALESCE(s.tvdb_id, '') AS tvdb_id
	FROM notification_deliveries d
	LEFT JOIN episodes e ON e.content_id = d.episode_id
	LEFT JOIN media_items s ON s.content_id = d.series_id`

func scanDeliveryRows(rows pgx.Rows) ([]DeliveryRow, error) {
	defer rows.Close()
	out := make([]DeliveryRow, 0, 25)
	for rows.Next() {
		var row DeliveryRow
		if err := rows.Scan(
			&row.ID, &row.ReleaseEventID, &row.UserID, &row.ProfileID,
			&row.LibraryID, &row.SeriesID, &row.EpisodeID,
			&row.Type, &row.ReasonFlags, &row.Status, &row.ReadAt, &row.DeliveredAt, &row.CreatedAt,
			&row.Body, &row.ExpiresAt, &row.DismissedAt, &row.AnnouncementID,
			&row.SeriesTitle, &row.EpisodeTitle, &row.SeasonNumber, &row.EpisodeNumber,
			&row.PosterPath, &row.PosterThumbhash, &row.PosterSourcePath,
			&row.MediaType, &row.Year, &row.SeriesOverview, &row.EpisodeOverview,
			&row.Genres, &row.ContentRating, &row.RatingIMDB, &row.RatingTMDB,
			&row.IMDBID, &row.TMDBID, &row.TVDBID,
		); err != nil {
			return nil, fmt.Errorf("scan delivery row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// BulkInsert inserts deliveries with ON CONFLICT DO NOTHING (both partial
// uniques participate: per-release-event and cross-library per-episode) and
// returns only the rows actually inserted. Realtime publish and channel
// dispatch must operate on the returned set, never the candidate set.
func (r *DeliveryRepository) BulkInsert(ctx context.Context, tx pgx.Tx, deliveries []Delivery) ([]InsertedDelivery, error) {
	const chunkSize = 500
	inserted := make([]InsertedDelivery, 0, len(deliveries))
	for start := 0; start < len(deliveries); start += chunkSize {
		end := min(start+chunkSize, len(deliveries))
		chunk := deliveries[start:end]

		var sb strings.Builder
		sb.WriteString(`
			INSERT INTO notification_deliveries
				(id, release_event_id, user_id, profile_id, library_id, series_id, episode_id,
				 type, reason_flags, status, delivered_at, body, expires_at, announcement_id)
			VALUES `)
		args := make([]any, 0, len(chunk)*14)
		for i, delivery := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args)
			fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11,
				base+12, base+13, base+14)
			status := delivery.Status
			if status == "" {
				status = "delivered"
			}
			reasonFlags := delivery.ReasonFlags
			if len(reasonFlags) == 0 {
				reasonFlags = []byte("{}")
			}
			// expires_at is derived from the body here and nowhere else, so
			// the filter column can never disagree with the payload.
			expiresAt := delivery.ExpiresAt
			if body, ok := ParseAlertBody(delivery.Body); ok && body.ExpiresAt != nil {
				expiresAt = body.ExpiresAt
			}
			args = append(args,
				delivery.ID, delivery.ReleaseEventID, delivery.UserID, delivery.ProfileID,
				delivery.LibraryID, delivery.SeriesID, delivery.EpisodeID,
				delivery.Type, reasonFlags, status, time.Now().UTC(),
				delivery.Body, expiresAt, delivery.AnnouncementID,
			)
		}
		sb.WriteString(" ON CONFLICT DO NOTHING RETURNING id, user_id, profile_id, created_at")

		rows, err := tx.Query(ctx, sb.String(), args...)
		if err != nil {
			return nil, fmt.Errorf("bulk insert deliveries: %w", err)
		}
		for rows.Next() {
			var row InsertedDelivery
			if err := rows.Scan(&row.ID, &row.UserID, &row.ProfileID, &row.CreatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan inserted delivery: %w", err)
			}
			inserted = append(inserted, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read inserted deliveries: %w", err)
		}
	}
	return inserted, nil
}

// Visibility predicates shared by every client-facing read. Expired rows
// are never served (AMENDMENT 2); dismissed rows are hidden unless the
// caller opts in. Digest and outbox readers apply the expiry rule too so a
// lapsed alert is not mailed after the fact.
const (
	deliveryNotExpired   = "(d.expires_at IS NULL OR d.expires_at > now())"
	deliveryNotDismissed = "d.dismissed_at IS NULL"
)

func deliveryVisibility(includeDismissed bool) string {
	if includeDismissed {
		return deliveryNotExpired
	}
	return deliveryNotExpired + " AND " + deliveryNotDismissed
}

// ListInbox returns inbox rows newest-first for the profile. Expired rows
// are always filtered; dismissed rows only when includeDismissed is false.
func (r *DeliveryRepository) ListInbox(ctx context.Context, profileID string, unreadOnly, includeDismissed bool, limit int, before *Cursor) ([]DeliveryRow, error) {
	conditions := []string{"d.profile_id = $1", deliveryVisibility(includeDismissed)}
	args := []any{profileID}
	if unreadOnly {
		conditions = append(conditions, "d.read_at IS NULL")
	}
	if before != nil {
		args = append(args, before.CreatedAt, before.ID)
		conditions = append(conditions, fmt.Sprintf("(d.created_at, d.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit)
	query := deliveryRowSelect +
		" WHERE " + strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY d.created_at DESC, d.id DESC LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list inbox: %w", err)
	}
	return scanDeliveryRows(rows)
}

// ListSync returns rows ascending from the cursor for forward sync (the
// mobile wake-fetch endpoint). A nil cursor returns the most recent page
// (still ascending) so first-time callers get a cursor to persist.
func (r *DeliveryRepository) ListSync(ctx context.Context, profileID string, since *Cursor, includeDismissed bool, limit int) ([]DeliveryRow, error) {
	visibility := deliveryVisibility(includeDismissed)
	if since != nil {
		rows, err := r.pool.Query(ctx,
			deliveryRowSelect+`
			WHERE d.profile_id = $1 AND (d.created_at, d.id) > ($2, $3) AND `+visibility+`
			ORDER BY d.created_at ASC, d.id ASC
			LIMIT $4`,
			profileID, since.CreatedAt, since.ID, limit)
		if err != nil {
			return nil, fmt.Errorf("list sync: %w", err)
		}
		return scanDeliveryRows(rows)
	}
	// No cursor: most recent page, returned in ascending order.
	rows, err := r.pool.Query(ctx, `
		SELECT * FROM (`+deliveryRowSelect+`
			WHERE d.profile_id = $1 AND `+visibility+`
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT $2
		) recent ORDER BY recent.created_at ASC, recent.id ASC`,
		profileID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sync: %w", err)
	}
	return scanDeliveryRows(rows)
}

// GetByID returns one delivery scoped to the profile; (nil, nil) when absent.
func (r *DeliveryRepository) GetByID(ctx context.Context, profileID, id string) (*DeliveryRow, error) {
	rows, err := r.pool.Query(ctx,
		deliveryRowSelect+` WHERE d.profile_id = $1 AND d.id = $2`, profileID, id)
	if err != nil {
		return nil, fmt.Errorf("get delivery: %w", err)
	}
	out, err := scanDeliveryRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// GetRowByID loads one delivery without profile scoping. Internal use only
// (webhook attempt processing); API paths must use GetByID.
func (r *DeliveryRepository) GetRowByID(ctx context.Context, id string) (*DeliveryRow, error) {
	rows, err := r.pool.Query(ctx, deliveryRowSelect+` WHERE d.id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get delivery row: %w", err)
	}
	out, err := scanDeliveryRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// ListForUserSince returns the account's deliveries newer than the watermark,
// ascending, across all of its profiles. A non-zero until excludes rows
// created at or after it (digest window upper edge). Runs inside the channel
// worker's claim transaction so the rows read are the rows the advanced
// watermark covers.
func (r *DeliveryRepository) ListForUserSince(ctx context.Context, tx pgx.Tx, userID int, since Cursor, until time.Time, limit int) ([]DeliveryRow, error) {
	query := deliveryRowSelect + `
		WHERE d.user_id = $1 AND (d.created_at, d.id) > ($2, $3) AND ` + deliveryNotExpired
	args := []any{userID, since.CreatedAt, since.ID}
	if !until.IsZero() {
		args = append(args, until)
		query += fmt.Sprintf(" AND d.created_at < $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY d.created_at ASC, d.id ASC LIMIT $%d", len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list user deliveries since: %w", err)
	}
	return scanDeliveryRows(rows)
}

// HasForUserSince reports whether the account has any delivery newer than the
// given watermark. Cheap pre-check (index-only) so account-channel sweeps do
// not open a claim transaction for idle accounts every pass.
func (r *DeliveryRepository) HasForUserSince(ctx context.Context, userID int, since Cursor) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notification_deliveries
			WHERE user_id = $1 AND (created_at, id) > ($2, $3)
		)`,
		userID, since.CreatedAt, since.ID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check deliveries since watermark: %w", err)
	}
	return exists, nil
}

// HasTransactionalForUserSince reports whether the account has any
// transactional delivery (digest-bypassing request-status notice; see
// transactionalDeliveryTypes) newer than the given watermark.
func (r *DeliveryRepository) HasTransactionalForUserSince(ctx context.Context, userID int, since Cursor) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notification_deliveries
			WHERE user_id = $1 AND (created_at, id) > ($2, $3) AND type = ANY($4)
		)`,
		userID, since.CreatedAt, since.ID, transactionalDeliveryTypes,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check transactional deliveries since watermark: %w", err)
	}
	return exists, nil
}

// ListForProfileSince returns the profile's deliveries newer than the
// watermark, ascending. A non-zero until excludes rows created at or after it
// (digest window upper edge). Runs inside the channel worker's claim
// transaction so the rows read are the rows the advanced watermark covers.
func (r *DeliveryRepository) ListForProfileSince(ctx context.Context, tx pgx.Tx, profileID string, since Cursor, until time.Time, limit int) ([]DeliveryRow, error) {
	query := deliveryRowSelect + `
		WHERE d.profile_id = $1 AND (d.created_at, d.id) > ($2, $3) AND ` + deliveryNotExpired
	args := []any{profileID, since.CreatedAt, since.ID}
	if !until.IsZero() {
		args = append(args, until)
		query += fmt.Sprintf(" AND d.created_at < $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY d.created_at ASC, d.id ASC LIMIT $%d", len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list profile deliveries since: %w", err)
	}
	return scanDeliveryRows(rows)
}

// HasForProfileSince reports whether the profile has any delivery newer than
// the given watermark. Cheap pre-check (index-only) so account-channel sweeps
// do not open a claim transaction for idle profiles every pass.
func (r *DeliveryRepository) HasForProfileSince(ctx context.Context, profileID string, since Cursor) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notification_deliveries
			WHERE profile_id = $1 AND (created_at, id) > ($2, $3)
		)`,
		profileID, since.CreatedAt, since.ID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check profile deliveries since watermark: %w", err)
	}
	return exists, nil
}

// HasTransactionalForProfileSince reports whether the profile has any
// transactional delivery (digest-bypassing request-status notice; see
// transactionalDeliveryTypes) newer than the given watermark.
func (r *DeliveryRepository) HasTransactionalForProfileSince(ctx context.Context, profileID string, since Cursor) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notification_deliveries
			WHERE profile_id = $1 AND (created_at, id) > ($2, $3) AND type = ANY($4)
		)`,
		profileID, since.CreatedAt, since.ID, transactionalDeliveryTypes,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check profile transactional deliveries since watermark: %w", err)
	}
	return exists, nil
}

// RecentUnread returns the newest unread rows for the websocket snapshot.
func (r *DeliveryRepository) RecentUnread(ctx context.Context, profileID string, limit int) ([]DeliveryRow, error) {
	rows, err := r.pool.Query(ctx,
		deliveryRowSelect+`
		WHERE d.profile_id = $1 AND d.read_at IS NULL AND `+deliveryVisibility(false)+`
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $2`,
		profileID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent unread: %w", err)
	}
	return scanDeliveryRows(rows)
}

// UnreadCount returns the unread badge count for the profile.
func (r *DeliveryRepository) UnreadCount(ctx context.Context, profileID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM notification_deliveries d
		 WHERE d.profile_id = $1 AND d.read_at IS NULL AND `+deliveryVisibility(false),
		profileID,
	).Scan(&count)
	return count, err
}

// MarkRead marks one delivery read. Idempotent; reports whether the row
// transitioned from unread to read.
func (r *DeliveryRepository) MarkRead(ctx context.Context, profileID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET read_at = now()
		WHERE profile_id = $1 AND id = $2 AND read_at IS NULL`,
		profileID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ErrDeliveryNotDismissible reports a dismiss attempt on a row whose body
// carries dismissible=false (every critical alert).
var ErrDeliveryNotDismissible = errors.New("notification is not dismissible")

// Dismiss hides one delivery from the profile's feeds without marking it
// read. Idempotent; reports whether the row transitioned. Rows whose body
// says dismissible=false are refused with ErrDeliveryNotDismissible.
func (r *DeliveryRepository) Dismiss(ctx context.Context, profileID, id string) (bool, error) {
	var dismissible *bool
	var alreadyDismissed bool
	err := r.pool.QueryRow(ctx, `
		SELECT (body->>'dismissible')::boolean, dismissed_at IS NOT NULL
		FROM notification_deliveries WHERE profile_id = $1 AND id = $2`,
		profileID, id).Scan(&dismissible, &alreadyDismissed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load delivery for dismiss: %w", err)
	}
	if dismissible != nil && !*dismissible {
		return false, ErrDeliveryNotDismissible
	}
	if alreadyDismissed {
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET dismissed_at = now()
		WHERE profile_id = $1 AND id = $2 AND dismissed_at IS NULL`,
		profileID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetRowsByIDs loads deliveries without profile scoping, for post-commit
// dispatch of a batch. Order is unspecified.
func (r *DeliveryRepository) GetRowsByIDs(ctx context.Context, ids []string) ([]DeliveryRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, deliveryRowSelect+` WHERE d.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("get delivery rows: %w", err)
	}
	return scanDeliveryRows(rows)
}

// WithdrawnDelivery identifies a row removed or expired by an announcement
// withdrawal so realtime clients can drop it.
type WithdrawnDelivery struct {
	ID        string
	UserID    int
	ProfileID string
}

// WithdrawAnnouncement removes the announcement's unread rows (their pending
// outbox attempts cascade away, so undelivered pushes are cancelled) and
// expires the already-read ones so every feed stops showing them. Returns
// both sets for realtime notification.
func (r *DeliveryRepository) WithdrawAnnouncement(ctx context.Context, tx pgx.Tx, announcementID string) ([]WithdrawnDelivery, error) {
	rows, err := tx.Query(ctx, `
		WITH removed AS (
			DELETE FROM notification_deliveries
			WHERE announcement_id = $1 AND read_at IS NULL
			RETURNING id, user_id, profile_id
		), expired AS (
			UPDATE notification_deliveries
			SET expires_at = now()
			WHERE announcement_id = $1 AND read_at IS NOT NULL
			  AND (expires_at IS NULL OR expires_at > now())
			RETURNING id, user_id, profile_id
		)
		SELECT id, user_id, profile_id FROM removed
		UNION ALL
		SELECT id, user_id, profile_id FROM expired`, announcementID)
	if err != nil {
		return nil, fmt.Errorf("withdraw announcement deliveries: %w", err)
	}
	defer rows.Close()
	out := make([]WithdrawnDelivery, 0, 16)
	for rows.Next() {
		var row WithdrawnDelivery
		if err := rows.Scan(&row.ID, &row.UserID, &row.ProfileID); err != nil {
			return nil, fmt.Errorf("scan withdrawn delivery: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Exists reports whether a delivery belongs to the profile (used to make
// mark-read idempotent without leaking other profiles' IDs).
func (r *DeliveryRepository) Exists(ctx context.Context, profileID, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM notification_deliveries WHERE profile_id = $1 AND id = $2)`,
		profileID, id,
	).Scan(&exists)
	return exists, err
}

// MarkAllRead marks every unread delivery read for the profile.
func (r *DeliveryRepository) MarkAllRead(ctx context.Context, profileID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET read_at = now()
		WHERE profile_id = $1 AND read_at IS NULL`,
		profileID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteAllForProfile removes every delivery for a deleted profile (profiles
// may live outside Postgres, so no cascade).
func (r *DeliveryRepository) DeleteAllForProfile(ctx context.Context, profileID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM notification_deliveries WHERE profile_id = $1`, profileID)
	return err
}

// DeleteOld applies retention: rows read longer ago than readCutoff, unread
// rows created before unreadCutoff. Read rows age from read_at, not
// created_at — an old notification read today starts a fresh read window.
func (r *DeliveryRepository) DeleteOld(ctx context.Context, readCutoff, unreadCutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM notification_deliveries
		WHERE (read_at IS NOT NULL AND read_at < $1)
		   OR (read_at IS NULL AND created_at < $2)`,
		readCutoff, unreadCutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
