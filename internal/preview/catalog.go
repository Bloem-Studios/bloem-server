package preview

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// CatalogItem contains metadata only; the picker never prepares playback
// files, mints grants, or calls an external search or metadata provider.
type CatalogItem struct {
	ContentID string `json:"content_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Overview  string `json:"overview"`
}

func (a *Admin) Catalog(ctx context.Context, actor int, query, provider, providerID string) ([]CatalogItem, error) {
	if len(query) > 256 || len(providerID) > 128 || (provider == "") != (providerID == "") {
		return nil, ErrInvalidSettings
	}
	if provider != "" && provider != "tmdb" && provider != "imdb" && provider != "tvdb" {
		return nil, fmt.Errorf("%w: unsupported provider", ErrInvalidSettings)
	}
	s, err := a.Settings(ctx, actor)
	if err != nil {
		return nil, err
	}
	var libraries []int
	var profileID string
	err = a.pool.QueryRow(ctx, `SELECT ARRAY(SELECT f.id FROM media_folders f WHERE f.enabled AND f.id=ANY(g.library_ids) ORDER BY f.id),p.id FROM users u
 JOIN organization_memberships m ON m.account_id=u.id AND m.status='active'
 JOIN organizations o ON o.id=m.organization_id AND o.is_default
 JOIN access_groups g ON g.id=u.access_group_id AND g.organization_id=m.organization_id AND g.managed_template_key='bloem-preview'
 JOIN user_profiles p ON p.user_id=u.id AND p.is_primary AND p.organization_id=m.organization_id AND p.access_group_id=g.id
 WHERE u.id=$1 AND u.enabled AND u.role='user' AND NOT u.local_password_login_enabled AND g.library_ids IS NOT NULL`, s.AccountID).Scan(&libraries, &profileID)
	if err != nil || libraries == nil {
		return nil, ErrUnavailable
	}
	filter := catalog.AccessFilter{AllowedLibraryIDs: libraries, UserID: s.AccountID, ProfileID: profileID,
		MaturityLimits: access.MaturityLimits{MaxContentRating: s.MaturityCeiling}}
	repo := catalog.NewItemRepository(a.pool)
	if provider != "" {
		rows, err := a.pool.Query(ctx, `SELECT content_id FROM media_item_provider_ids WHERE provider=$1 AND provider_id=$2
 UNION SELECT content_id FROM media_items WHERE ($1='tmdb' AND tmdb_id=$2) OR ($1='imdb' AND imdb_id=$2) OR ($1='tvdb' AND tvdb_id=$2) LIMIT 1000`, provider, providerID)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []CatalogItem{}, nil
		}
		items, err := repo.GetByIDsWithAccess(ctx, ids, filter)
		if err != nil {
			return nil, err
		}
		out := []CatalogItem{}
		for _, item := range items {
			if item.Type != "movie" && item.Type != "series" && item.Type != "audiobook" && item.Type != "ebook" {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(item.Title), strings.ToLower(strings.TrimSpace(query))) {
				continue
			}
			out = append(out, CatalogItem{ContentID: item.ContentID, Kind: item.Type, Title: item.Title, Year: item.Year, Overview: item.Overview})
			if len(out) == 50 {
				break
			}
		}
		return out, nil
	}
	items, _, err := repo.Search(ctx, strings.TrimSpace(query), []string{"movie", "series", "audiobook", "ebook"}, 50, 0, filter)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItem, 0, len(items))
	for _, item := range items {
		out = append(out, CatalogItem{ContentID: item.ContentID, Kind: item.Type, Title: item.Title, Year: item.Year, Overview: item.Overview})
	}
	return out, nil
}
