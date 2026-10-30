package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// BloemNewArrival is one film added, or one series with episodes added, in
// the window.
type BloemNewArrival struct {
	ContentID string    `json:"content_id"`
	Kind      string    `json:"kind" enum:"movie,series"`
	Title     string    `json:"title"`
	Year      *int      `json:"year,omitempty"`
	AddedAt   time.Time `json:"added_at" doc:"When the film, or the series' newest episode, was first seen in a chosen library"`
	// NewEpisodes counts a series' episodes first seen in the window.
	NewEpisodes int `json:"new_episodes" minimum:"0"`
	// NewSeries is true when the series itself first appeared in the window.
	NewSeries bool `json:"new_series"`
}

// BloemNewArrivals lists recent additions, newest first.
type BloemNewArrivals struct {
	Since time.Time         `json:"since" doc:"Start of the window (UTC)"`
	Items []BloemNewArrival `json:"items" nullable:"false"`
}

// BloemNewArrivalsHandler serves recent additions to chosen libraries, which a
// storefront mails to its customers as "new this week". It reads titles and
// years only: no paths, files or accounts.
type BloemNewArrivalsHandler struct{ pool *pgxpool.Pool }

func NewBloemNewArrivalsHandler(pool *pgxpool.Pool) *BloemNewArrivalsHandler {
	return &BloemNewArrivalsHandler{pool: pool}
}

var errNewArrivalsForbidden = errors.New("new arrivals: platform administrator required")

type newArrivalsQuery struct {
	days      int
	limit     int
	libraries []int32
}

func parseNewArrivalsQuery(r *http.Request) (newArrivalsQuery, bool) {
	q := newArrivalsQuery{days: 7, limit: 20}
	for name, values := range r.URL.Query() {
		switch name {
		case "days", "limit":
			if len(values) != 1 {
				return q, false
			}
			n, err := strconv.Atoi(values[0])
			if err != nil {
				return q, false
			}
			if name == "days" {
				if n < 1 || n > 31 {
					return q, false
				}
				q.days = n
			} else {
				if n < 1 || n > 50 {
					return q, false
				}
				q.limit = n
			}
		case "library":
			for _, value := range values {
				n, err := strconv.ParseInt(value, 10, 32)
				if err != nil || n < 1 {
					return q, false
				}
				q.libraries = append(q.libraries, int32(n))
			}
		default:
			return q, false
		}
	}
	return q, true
}

func (h *BloemNewArrivalsHandler) actor(w http.ResponseWriter, r *http.Request) (int, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok {
		if claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0 {
			return claims.AccountID, true
		}
	} else if c := middleware.GetClaims(r.Context()); c != nil && c.TokenType == auth.TokenTypeAPIKey && c.Role == "admin" && c.UserID > 0 && len(c.APIKeyScopes) == 0 {
		return c.UserID, true
	}
	writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	return 0, false
}

func (h *BloemNewArrivalsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	query, ok := parseNewArrivalsQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "days must be 1 to 31, limit 1 to 50, and each library a library ID.")
		return
	}
	out, err := h.read(r.Context(), actor, query)
	switch {
	case errors.Is(err, errNewArrivalsForbidden):
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "new_arrivals_unavailable", "New arrivals are unavailable.")
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// read lists, in one read-only snapshot, films first seen in an enabled chosen
// library inside the window and series with episodes first seen there. With
// no library chosen, every enabled library counts.
func (h *BloemNewArrivalsHandler) read(ctx context.Context, actor int, q newArrivalsQuery) (BloemNewArrivals, error) {
	if h.pool == nil {
		return BloemNewArrivals{}, errors.New("new arrivals: no database")
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return BloemNewArrivals{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'`); err != nil {
		return BloemNewArrivals{}, err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(enabled, false) AND role = 'admin' FROM users WHERE id = $1`, actor).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !eligible {
		return BloemNewArrivals{}, errNewArrivalsForbidden
	}
	if err != nil {
		return BloemNewArrivals{}, err
	}
	out := BloemNewArrivals{Items: []BloemNewArrival{}}
	if err := tx.QueryRow(ctx, `SELECT now() - make_interval(days => $1)`, q.days).Scan(&out.Since); err != nil {
		return BloemNewArrivals{}, err
	}
	out.Since = out.Since.UTC()
	var libraries []int32
	if len(q.libraries) > 0 {
		libraries = q.libraries
	}
	rows, err := tx.Query(ctx, `
		WITH chosen AS (
			SELECT id FROM media_folders
			WHERE COALESCE(enabled, true) AND ($2::int[] IS NULL OR id = ANY($2::int[]))
		), films AS (
			SELECT mi.content_id, 'movie' AS kind, mi.title, mi.year,
			       min(mil.first_seen_at) AS added_at, 0 AS new_episodes, false AS new_series
			FROM media_item_libraries mil
			JOIN chosen c ON c.id = mil.media_folder_id
			JOIN media_items mi ON mi.content_id = mil.content_id AND mi.type = 'movie'
			GROUP BY mi.content_id, mi.title, mi.year
			HAVING min(mil.first_seen_at) >= $1
		), shows AS (
			SELECT s.content_id, 'series' AS kind, s.title, s.year,
			       max(el.first_seen_at) AS added_at, count(DISTINCT el.episode_id)::int AS new_episodes,
			       COALESCE((SELECT min(sl.first_seen_at) FROM media_item_libraries sl
			                 JOIN chosen c2 ON c2.id = sl.media_folder_id
			                 WHERE sl.content_id = s.content_id), $1) >= $1 AS new_series
			FROM episode_libraries el
			JOIN chosen c ON c.id = el.media_folder_id
			JOIN episodes e ON e.content_id = el.episode_id
			JOIN media_items s ON s.content_id = e.series_id
			WHERE el.first_seen_at >= $1
			GROUP BY s.content_id, s.title, s.year
		)
		SELECT content_id, kind, title, year, added_at, new_episodes, new_series
		FROM (SELECT * FROM films UNION ALL SELECT * FROM shows) arrivals
		ORDER BY added_at DESC, content_id
		LIMIT $3`, out.Since, libraries, q.limit)
	if err != nil {
		return BloemNewArrivals{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item BloemNewArrival
		if err := rows.Scan(&item.ContentID, &item.Kind, &item.Title, &item.Year, &item.AddedAt, &item.NewEpisodes, &item.NewSeries); err != nil {
			return BloemNewArrivals{}, err
		}
		item.AddedAt = item.AddedAt.UTC()
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
