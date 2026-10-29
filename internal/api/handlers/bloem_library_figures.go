package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// BloemLibraryFigure is one library's item counts. Movies, series, audiobooks
// and ebooks are catalog items listed in the library; episodes are episodes
// listed in it. An item in two libraries counts in both.
type BloemLibraryFigure struct {
	LibraryID  int64  `json:"library_id" example:"1"`
	Name       string `json:"name" example:"Movies"`
	Type       string `json:"type" doc:"Library kind (movies, series, mixed, audiobooks, ebooks, ...)" example:"movies"`
	Enabled    bool   `json:"enabled"`
	Movies     int64  `json:"movies" minimum:"0"`
	Series     int64  `json:"series" minimum:"0"`
	Episodes   int64  `json:"episodes" minimum:"0"`
	Audiobooks int64  `json:"audiobooks" minimum:"0"`
	Ebooks     int64  `json:"ebooks" minimum:"0"`
}

// BloemLibraryFigures is every library's counts, read in one snapshot.
type BloemLibraryFigures struct {
	CountedAt time.Time            `json:"counted_at" doc:"When the snapshot was read (UTC)"`
	Libraries []BloemLibraryFigure `json:"libraries" nullable:"false" doc:"Every library, in the server's library order"`
}

// errLibraryFiguresForbidden means the actor is no longer an enabled
// administrator.
var errLibraryFiguresForbidden = errors.New("library figures: platform administrator required")

// BloemLibraryFiguresHandler serves aggregate item counts per library, the
// figures a storefront publishes on its selling page. It returns counts only:
// no titles, paths or accounts.
type BloemLibraryFiguresHandler struct{ pool *pgxpool.Pool }

func NewBloemLibraryFiguresHandler(pool *pgxpool.Pool) *BloemLibraryFiguresHandler {
	return &BloemLibraryFiguresHandler{pool: pool}
}

// actor accepts a Platform administrative context or an administrator's
// unscoped API key. A scoped key never reaches here: no scope lists this route.
func (h *BloemLibraryFiguresHandler) actor(w http.ResponseWriter, r *http.Request) (int, bool) {
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

func (h *BloemLibraryFiguresHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if len(r.URL.RawQuery) > 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "This request takes no query parameters.")
		return
	}
	figures, err := h.read(r.Context(), actor)
	switch {
	case errors.Is(err, errLibraryFiguresForbidden):
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "library_figures_unavailable", "Library figures are unavailable.")
	default:
		writeJSON(w, http.StatusOK, figures)
	}
}

// read counts every library in one read-only snapshot, after rechecking that
// the actor is still an enabled administrator. A full count of a large
// catalog takes about a second, so callers cache it rather than poll.
func (h *BloemLibraryFiguresHandler) read(ctx context.Context, actor int) (BloemLibraryFigures, error) {
	if h.pool == nil {
		return BloemLibraryFigures{}, errors.New("library figures: no database")
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return BloemLibraryFigures{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'`); err != nil {
		return BloemLibraryFigures{}, err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(enabled, false) AND role = 'admin' FROM users WHERE id = $1`, actor).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !eligible {
		return BloemLibraryFigures{}, errLibraryFiguresForbidden
	}
	if err != nil {
		return BloemLibraryFigures{}, err
	}
	out := BloemLibraryFigures{Libraries: []BloemLibraryFigure{}}
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&out.CountedAt); err != nil {
		return BloemLibraryFigures{}, err
	}
	out.CountedAt = out.CountedAt.UTC()
	rows, err := tx.Query(ctx, `
		SELECT f.id, f.name, f.type, COALESCE(f.enabled, true),
		       COALESCE(i.movies, 0), COALESCE(i.series, 0), COALESCE(e.episodes, 0),
		       COALESCE(i.audiobooks, 0), COALESCE(i.ebooks, 0)
		FROM media_folders f
		LEFT JOIN (
			SELECT mil.media_folder_id,
			       count(*) FILTER (WHERE mi.type = 'movie') AS movies,
			       count(*) FILTER (WHERE mi.type = 'series') AS series,
			       count(*) FILTER (WHERE mi.type = 'audiobook') AS audiobooks,
			       count(*) FILTER (WHERE mi.type = 'ebook') AS ebooks
			FROM media_item_libraries mil
			JOIN media_items mi ON mi.content_id = mil.content_id
			GROUP BY mil.media_folder_id
		) i ON i.media_folder_id = f.id
		LEFT JOIN (
			SELECT media_folder_id, count(*) AS episodes
			FROM episode_libraries
			GROUP BY media_folder_id
		) e ON e.media_folder_id = f.id
		ORDER BY f.sort_order, f.id`)
	if err != nil {
		return BloemLibraryFigures{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var f BloemLibraryFigure
		if err := rows.Scan(&f.LibraryID, &f.Name, &f.Type, &f.Enabled, &f.Movies, &f.Series, &f.Episodes, &f.Audiobooks, &f.Ebooks); err != nil {
			return BloemLibraryFigures{}, err
		}
		out.Libraries = append(out.Libraries, f)
	}
	if err := rows.Err(); err != nil {
		return BloemLibraryFigures{}, err
	}
	return out, nil
}
