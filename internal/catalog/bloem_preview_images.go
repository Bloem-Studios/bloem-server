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
// A database lookup failure refuses artwork for previews.
func BloemPreviewImages(next ImageResolver, pool *pgxpool.Pool) ImageResolver {
	if next == nil {
		return nil
	}
	return &bloemPreviewImages{next: next, pool: pool}
}
func (r *bloemPreviewImages) allowed(ctx context.Context, paths []string) []string {
	if !previewcontext.Active(ctx) {
		return paths
	}
	candidates := make([]string, 0, len(paths))
	dirs := make([]string, 0, len(paths))
	for _, p := range paths {
		key := p
		if _, rest, ok := strings.Cut(key, "://"); ok {
			key = rest
		}
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "chapter-images/") || strings.HasPrefix(lower, "marker-images/") || strings.HasPrefix(lower, "chapters/") || strings.HasPrefix(lower, "markers/") || strings.HasPrefix(lower, "trickplay/") || strings.HasPrefix(lower, "frames/") || artworkkey.IsLocal(key) && artworkkey.ImageTypeFromPath(key) != artworkkey.ImagePoster {
			continue
		}
		candidates = append(candidates, p)
		dirs = append(dirs, path.Dir(p))
	}
	if len(candidates) == 0 || r.pool == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `SELECT poster_path FROM media_items WHERE type='manga_chapter' AND poster_path<>'' AND (poster_path=ANY($1) OR regexp_replace(poster_path,'/[^/]+$','')=ANY($2))`, candidates, dirs)
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
