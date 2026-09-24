package catalog

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
)

// PersonItemRole is one person's credited role on a single item — the kind
// (actor, director, ...) and, for an acting credit, the character name.
//
// This is deliberately a different, smaller type than the item-detail API's
// PersonCredit (detail.go): that one carries a person's full identity
// (name, external ids, photo) for an item's cast/crew list; this one carries
// only the role, for callers that already know the person and just need to
// know what they did on a specific item.
type PersonItemRole struct {
	Kind      models.PersonKind
	Character string
}

// RolesForItems returns personID's credited role on each of the given items,
// keyed by content_id. An item where the person has more than one credit
// (e.g. an actor who also directed) reports only the one with the lowest
// sort_order — the same "one credit represents the item" choice a filmography
// entry needs, since it lists items rather than per-item credit rows. An item
// where the person has no credit is simply absent from the result rather than
// an error: the caller already knows which items to ask about from a browse
// query keyed on this same person, so a missing row only means the browse
// query and item_people briefly disagree (e.g. a credit removed between the
// two reads).
func (r *PersonRepository) RolesForItems(ctx context.Context, personID int64, contentIDs []string) (map[string]PersonItemRole, error) {
	result := make(map[string]PersonItemRole, len(contentIDs))
	if len(contentIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (content_id) content_id, kind, character
		FROM item_people
		WHERE person_id = $1 AND content_id = ANY($2)
		ORDER BY content_id, sort_order`, personID, contentIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("roles for items for person %d: %w", personID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var contentID string
		var role PersonItemRole
		if err := rows.Scan(&contentID, &role.Kind, &role.Character); err != nil {
			return nil, fmt.Errorf("scan person credit: %w", err)
		}
		result[contentID] = role
	}
	return result, rows.Err()
}
