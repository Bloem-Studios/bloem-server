package music

import (
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// albumVisibility returns the SQL predicate (a parenthesized AND of
// conditions, never empty) that gates one music album for a viewer, binding
// its arguments onto args. albumCol names the album's content_id column in
// the calling query and libraryArg the placeholder holding the requested
// library id.
//
// It applies what every other catalog read applies and the music queries
// previously skipped:
//   - the requested library must be enabled (media_folders.enabled), the same
//     gate Status uses;
//   - the canonical per-item library allow/deny membership predicates
//     (catalog.ApplyLibraryAccessFilter), so an album that is also a member of
//     a globally disabled library stays hidden;
//   - the viewer's rating ceiling (MaxContentRating) on the album's
//     media_items row, with the catalog's semantics: a ceiling that permits no
//     rating, or an unrated album under a ceiling, is not visible;
//   - a content allow-list (AllowedContentIDs), when one is set.
func albumVisibility(albumCol, libraryArg string, filter catalog.AccessFilter, args *[]any) string {
	conds := []string{
		fmt.Sprintf("EXISTS (SELECT 1 FROM media_folders vis_mf WHERE vis_mf.id = %s AND vis_mf.enabled = true)", libraryArg),
	}
	argIdx := len(*args) + 1
	catalog.ApplyLibraryAccessFilter(albumCol, filter, &conds, args, &argIdx)
	if filter.MaxContentRating != "" {
		ratings := access.AllowedRatingsUpTo(filter.MaxContentRating)
		if len(ratings) == 0 {
			conds = append(conds, "1 = 0")
		} else {
			*args = append(*args, ratings)
			conds = append(conds, fmt.Sprintf(
				"EXISTS (SELECT 1 FROM media_items vis_mi WHERE vis_mi.content_id = %s AND vis_mi.content_rating = ANY($%d))",
				albumCol, len(*args)))
		}
	}
	if filter.AllowedContentIDs != nil {
		*args = append(*args, filter.AllowedContentIDs)
		conds = append(conds, fmt.Sprintf("%s = ANY($%d)", albumCol, len(*args)))
	}
	return "(" + strings.Join(conds, " AND ") + ")"
}
