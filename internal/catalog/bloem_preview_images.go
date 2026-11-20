package catalog

import (
	"context"
	"path"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

type bloemPreviewImages struct {
	next ImageResolver
	pool *pgxpool.Pool
}

// BloemPreviewImages refuses file frames and manga chapter covers before signing.
// File frames are the chapter, marker and trickplay images and the stills Bloem
// takes from an extra's video. Library artwork (sidecar posters, backdrops,
// logos and episode thumbnails, cached under local/ and nfo/ keys) is not a
// frame and is signed as for a subscriber. A database lookup failure refuses
// artwork for previews.
func BloemPreviewImages(next ImageResolver, pool *pgxpool.Pool) ImageResolver {
	if next == nil {
		return nil
	}
	return &bloemPreviewImages{next: next, pool: pool}
}

// extraStillPrefix starts the key of every cached extra still. A still Bloem
// took from the extra's video shares the key layout of a sidecar one, so only
// the extra's still source tells them apart.
const extraStillPrefix = artworkkey.LocalPrefix + "extras/"

func (r *bloemPreviewImages) allowed(ctx context.Context, paths []string) []string {
	if !previewcontext.Active(ctx) {
		return paths
	}
	candidates := make([]string, 0, len(paths))
	dirs := make([]string, 0, len(paths))
	var extras, extraDirs []string
	for _, p := range paths {
		key := p
		if _, rest, ok := strings.Cut(key, "://"); ok {
			key = rest
		}
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "chapter-images/") || strings.HasPrefix(lower, "marker-images/") || strings.HasPrefix(lower, "chapters/") || strings.HasPrefix(lower, "markers/") || strings.HasPrefix(lower, "trickplay/") || strings.HasPrefix(lower, "frames/") {
			continue
		}
		candidates = append(candidates, p)
		dirs = append(dirs, path.Dir(p))
		if strings.HasPrefix(p, extraStillPrefix) {
			extras = append(extras, p)
			extraDirs = append(extraDirs, path.Dir(p))
		}
	}
	if len(candidates) == 0 || r.pool == nil {
		return nil
	}
	query := `SELECT poster_path FROM media_items WHERE type='manga_chapter' AND poster_path<>'' AND (poster_path=ANY($1) OR regexp_replace(poster_path,'/[^/]+$','')=ANY($2))`
	args := []any{candidates, dirs}
	if len(extras) > 0 {
		query += ` UNION ALL SELECT still_path FROM media_extras WHERE still_source LIKE $5 AND still_path<>'' AND (still_path=ANY($3) OR regexp_replace(still_path,'/[^/]+$','')=ANY($4))`
		args = append(args, extras, extraDirs, StillSourceFramePrefix+"%")
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	blocked := map[string]bool{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil {
			return nil
		}
		blocked[p] = true
		blocked[path.Dir(p)] = true
	}
	if rows.Err() != nil {
		return nil
	}
	allowed := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if !blocked[p] && !blocked[path.Dir(p)] {
			allowed = append(allowed, p)
		}
	}
	return allowed
}
func (r *bloemPreviewImages) ResolveImageURL(ctx context.Context, p, v string) string {
	return r.ResolveImageURLWithExpiry(ctx, p, v).URL
}
func (r *bloemPreviewImages) ResolveImageURLs(ctx context.Context, ps []string, v string) map[string]string {
	out := map[string]string{}
	for p, u := range r.ResolveImageURLsWithExpiry(ctx, ps, v) {
		out[p] = u.URL
	}
	return out
}
func (r *bloemPreviewImages) ResolveImageURLWithExpiry(ctx context.Context, p, v string) ResolvedImageURL {
	if len(r.allowed(ctx, []string{p})) == 0 {
		return ResolvedImageURL{}
	}
	if next, ok := r.next.(expiringImageResolver); ok {
		return next.ResolveImageURLWithExpiry(ctx, p, v)
	}
	return ResolvedImageURL{URL: r.next.ResolveImageURL(ctx, p, v)}
}
func (r *bloemPreviewImages) ResolveImageURLsWithExpiry(ctx context.Context, ps []string, v string) map[string]ResolvedImageURL {
	allowed := r.allowed(ctx, ps)
	if len(allowed) == 0 {
		return map[string]ResolvedImageURL{}
	}
	if next, ok := r.next.(expiringImageResolver); ok {
		return next.ResolveImageURLsWithExpiry(ctx, allowed, v)
	}
	out := map[string]ResolvedImageURL{}
	for p, u := range r.next.ResolveImageURLs(ctx, allowed, v) {
		out[p] = ResolvedImageURL{URL: u}
	}
	return out
}
