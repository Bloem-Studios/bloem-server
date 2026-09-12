package notifications

import "fmt"

// accountLevelDeliveryTypes is the explicit allowlist of delivery types
// eligible via deliveryAccessPredicate's account-level fallback (rule 3
// below): a row with no library and no item identity is eligible only if
// its type is listed here.
//
// This is deliberately a positive allowlist, not a negative exclusion of
// "types that require an item identity" (that was review finding 1 on the
// first version of this predicate): a delivery type that ships later and
// forgets to add itself here is NOT silently granted account-level
// eligibility. It falls through every branch of the predicate and is
// excluded — the conservative outcome — rather than reaching every profile
// in an active organization regardless of library access. Extending this
// list is the one explicit choice a new account-level type must make.
var accountLevelDeliveryTypes = []string{
	DeliveryTypeRequestApproved,
	DeliveryTypeRequestDeclined,
	DeliveryTypeWebhookAutoDisabled,
	DeliveryTypeSystemAlert,
	DeliveryTypeSystemAnnouncement,
}

// deliveryAccessPredicate returns a self-contained boolean SQL expression
// gating a notification_deliveries row (identified by alias, e.g. "d") by
// the recipient's CURRENT organization and library access. It references
// alias.profile_id, alias.library_id, alias.series_id, alias.episode_id and
// alias.type, which must be visible (and unambiguous) in the enclosing
// query, plus one bind parameter: argPos names the 1-based placeholder
// position the caller must bind to accountLevelDeliveryTypes (a []string).
// The registry is passed as a parameter array rather than interpolated into
// the query text, so it never grows the SQL string itself.
//
// This closes the authorization gap recorded in
// .superpowers/sdd/2026-09-12-v2-organization-enforcement/task-3-brief.md:
// profile_series_interest and notification_deliveries carry no ownership
// predicate at read time, so a revoked entitlement or a stale cross-tenant
// row previously survived fanout selection and queued delivery.
//
// Rules, evaluated in order; a row eligible under none of them is NOT
// eligible (fail closed):
//  1. the recipient's organization must be active, always;
//  2. a library-bound row (library_id set) is eligible only if the
//     recipient's organization currently owns that library, or holds an
//     ACTIVE entitlement to it (the same join shape as
//     resourcetenancy.Store.AvailableMediaFolderIDs);
//  3. a catalog-bound row with no fixed library (series_id or episode_id
//     set, e.g. request.fulfilled) is eligible only if the recipient's
//     organization can see at least one library containing the item — the
//     canonical item sitting in ANOTHER organization's private library does
//     not authorize the recipient;
//  4. a row with no library and no item identity is eligible only if its
//     type is in accountLevelDeliveryTypes (request approved/declined,
//     system alert/announcement, webhook auto-disabled today). A
//     request.fulfilled row with no item identity is malformed data (every
//     production writer sets one — see RequestFulfillmentNotifier.
//     NotifyFulfilled), and — like any type not on the allowlist — falls
//     through this branch too and is excluded, never reaching the
//     permissive default an unclassified type would otherwise get.
//
// Database errors surfaced while evaluating this predicate propagate as the
// enclosing query's error, so callers keep their existing retry behavior;
// this check runs before sending and can never recall an attempt already
// made.
func deliveryAccessPredicate(alias string, argPos int) string {
	return fmt.Sprintf(`(
		EXISTS (
			SELECT 1 FROM user_profiles dap_prof
			JOIN organizations dap_org ON dap_org.id = dap_prof.organization_id
			WHERE dap_prof.id = %[1]s.profile_id AND dap_org.status = 'active'
		)
		AND (
			(
				%[1]s.library_id IS NOT NULL
				AND EXISTS (
					SELECT 1
					FROM media_folders dap_folder
					JOIN resource_owners dap_owner ON dap_owner.id = dap_folder.owner_id
					JOIN user_profiles dap_prof2 ON dap_prof2.id = %[1]s.profile_id
					LEFT JOIN organization_entitlements dap_ent
					  ON dap_ent.organization_id = dap_prof2.organization_id
					 AND dap_ent.root_owner_id = dap_owner.id
					 AND dap_ent.media_folder_id = dap_folder.id
					 AND dap_ent.status = 'active'
					WHERE dap_folder.id = %[1]s.library_id
					  AND ((dap_owner.kind = 'organization' AND dap_owner.organization_id = dap_prof2.organization_id)
					       OR (dap_owner.kind = 'platform' AND dap_ent.id IS NOT NULL))
				)
			)
			OR (
				%[1]s.library_id IS NULL
				AND (%[1]s.series_id IS NOT NULL OR %[1]s.episode_id IS NOT NULL)
				AND EXISTS (
					SELECT 1
					FROM media_item_libraries dap_mil
					JOIN media_folders dap_folder ON dap_folder.id = dap_mil.media_folder_id
					JOIN resource_owners dap_owner ON dap_owner.id = dap_folder.owner_id
					JOIN user_profiles dap_prof2 ON dap_prof2.id = %[1]s.profile_id
					LEFT JOIN organization_entitlements dap_ent
					  ON dap_ent.organization_id = dap_prof2.organization_id
					 AND dap_ent.root_owner_id = dap_owner.id
					 AND dap_ent.media_folder_id = dap_folder.id
					 AND dap_ent.status = 'active'
					WHERE dap_mil.content_id = COALESCE(
						%[1]s.series_id,
						-- episode_id alone resolves to its parent series and
						-- checks series-level media_item_libraries, not the
						-- more granular episode_libraries table, which can
						-- diverge (an episode's file can sit in a library
						-- the series-level row does not list). Unreachable
						-- today: no production writer sets episode_id
						-- without series_id (request_notifier.go only sets
						-- SeriesID). A future writer that populates
						-- episode_id alone would only get series-level
						-- granularity here.
						(SELECT dap_ep.series_id FROM episodes dap_ep WHERE dap_ep.content_id = %[1]s.episode_id)
					)
					  AND ((dap_owner.kind = 'organization' AND dap_owner.organization_id = dap_prof2.organization_id)
					       OR (dap_owner.kind = 'platform' AND dap_ent.id IS NOT NULL))
				)
			)
			OR (
				%[1]s.library_id IS NULL AND %[1]s.series_id IS NULL AND %[1]s.episode_id IS NULL
				AND %[1]s.type = ANY($%[2]d)
			)
		)
	)`, alias, argPos)
}
