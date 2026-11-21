package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Bloem: moving a profile between the accounts of one household (a main
// account and its household logins), keeping the profile's id. See
// docs/architecture/bloem-household-logins.md, "Moving a person".
//
// Rows are keyed by (account, profile), and fifteen foreign keys reference
// user_profiles(user_id, id) with no ON UPDATE action and no deferral, so the
// profile row cannot simply change account. A move copies each parent row
// (the profile, the collections it owns, the shelf groups they sit in) under
// the target account with the same id, repoints the rows that belong to the
// person, ends what belonged to the old account's sign-ins, and deletes the
// old parent rows, all in the caller's transaction.

var (
	// ErrProfileMoveNotFound: the source account has no such profile.
	ErrProfileMoveNotFound = errors.New("profile move: the account has no such profile")
	// ErrProfileMoveLimit: the target account's profile limit has no room.
	ErrProfileMoveLimit = errors.New("profile move: the target account has no room for another profile")
	// ErrProfileMoveConflict: the target account already holds a profile with
	// this id, or already has a primary profile.
	ErrProfileMoveConflict = errors.New("profile move: the target account already has this profile or a primary profile")
)

type profileMoveKind int

const (
	// profileMoveCarry: the person's own data; the rows change account.
	profileMoveCarry profileMoveKind = iota
	// profileMoveEnd: the old account's sign-ins and devices as the
	// profile; the rows are deleted (or revoked, see Revoke).
	profileMoveEnd
	// profileMoveKeep: a record of what happened under the old account;
	// the rows stay where they are.
	profileMoveKeep
	// profileMoveFollows: keyed by the profile id alone, so the rows follow
	// the profile with nothing to change.
	profileMoveFollows
)

// profileMoveRule is what a move does with one table's rows of the profile.
type profileMoveRule struct {
	Kind                      profileMoveKind
	UserColumn, ProfileColumn string
	// Revoke: an ended row is revoked (revoked_at = now()) rather than
	// deleted, as a sign-out does.
	Revoke bool
	Reason string
}

func moveCarry(reason string) profileMoveRule {
	return profileMoveRule{Kind: profileMoveCarry, UserColumn: "user_id", ProfileColumn: "profile_id", Reason: reason}
}

func moveEnd(reason string) profileMoveRule {
	return profileMoveRule{Kind: profileMoveEnd, UserColumn: "user_id", ProfileColumn: "profile_id", Reason: reason}
}

func moveKeep(reason string) profileMoveRule {
	return profileMoveRule{Kind: profileMoveKeep, UserColumn: "user_id", ProfileColumn: "profile_id", Reason: reason}
}

func moveColumns(rule profileMoveRule, user, profile string) profileMoveRule {
	rule.UserColumn, rule.ProfileColumn = user, profile
	return rule
}

// bloemProfileMoveTables says what a move does with every table that has a
// profile column. TestProfileMoveCoversEveryProfileTable fails for a table
// with a column ending in profile_id that is neither here nor in
// bloemProfileMoveSteps.
var bloemProfileMoveTables = map[string]profileMoveRule{
	// Watching.
	"user_watch_progress":        moveCarry("where the person is in each title"),
	"user_watch_history":         moveCarry("what the person has watched"),
	"user_history_hidden_items":  moveCarry("history the person hid"),
	"user_favorites":             moveCarry("the person's favorites"),
	"user_watchlist":             moveCarry("the person's watchlist"),
	"user_watchlist_titles":      moveCarry("titles on the person's watchlist that are not in a library"),
	"watchlist_title_promotions": moveCarry("arrival notices of the person's watchlisted titles"),
	"user_ratings":               moveCarry("the person's ratings"),
	"user_not_interested":        moveCarry("titles the person is not interested in"),
	"user_dropped_series":        moveCarry("series the person dropped"),
	"user_home_item_dismissals":  moveCarry("home rows the person dismissed"),
	"user_taste_profiles":        moveCarry("the person's taste, derived from their ratings and history"),
	"user_taste_clusters":        moveCarry("the person's taste clusters"),
	"recommendation_cache":       moveCarry("the person's recommendations"),
	"profile_series_interest":    moveCarry("series the person follows for notifications"),
	// Settings.
	"user_setting_values":               moveCarry("the person's settings (profile, client-family, library and series scopes; account-scope rows have no profile)"),
	"user_audio_preferences":            moveCarry("the person's audio choices per series"),
	"user_subtitle_preferences":         moveCarry("the person's subtitle choices"),
	"user_series_playback_preferences":  moveCarry("the person's playback choices per series"),
	"user_library_playback_preferences": moveCarry("the person's playback choices per library"),
	"user_collection_sort_preferences":  moveCarry("the person's sort orders; those for collections that stay were removed first"),
	"user_profile_allowed_libraries":    moveCarry("the libraries the profile may see; a household's accounts share one plan group"),
	"user_profile_onboarding":           moveCarry("the tours the person has seen"),
	"profile_title_allowances":          moveCarry("titles allowed to a restricted profile"),
	"profile_collection_allowances":     moveCarry("collections allowed to a restricted profile; allowances across the accounts were removed first"),
	"user_personal_collection_profiles": moveCarry("visibility of the person's own collections; sharing with profiles that stay was removed first"),
	// Reading, listening, recording.
	"ebook_reader_progress":    moveCarry("where the person is in each book"),
	"ebook_reader_config":      moveCarry("the person's reader settings"),
	"ebook_reader_annotations": moveCarry("the person's highlights and notes"),
	"abs_bookmarks":            moveCarry("the person's audiobook bookmarks"),
	"abs_rss_feeds":            moveCarry("podcast feeds the person opened"),
	"livetv_series_rules":      moveCarry("the person's recording rules"),
	"livetv_recordings":        moveCarry("the person's recordings"),
	// Requests and notifications.
	"media_requests":                    moveColumns(moveCarry("titles the person requested; they keep following them"), "requested_by_user_id", "requested_by_profile_id"),
	"media_request_follows":             moveCarry("requests the person follows"),
	"bloem_book_requests":               moveColumns(moveCarry("books the person requested"), "requested_by_user_id", "requested_by_profile_id"),
	"notification_preferences":          moveCarry("the person's notification choices"),
	"profile_series_notification_mutes": moveCarry("series the person muted"),
	"notification_email_prefs":          moveCarry("where the person's email notifications go"),
	"notification_email_verifications":  moveCarry("the person's pending email verifications"),
	"notification_plugin_destinations":  moveCarry("the person's notification destinations"),
	"notification_webhooks":             moveCarry("the person's notification webhooks"),
	"notification_deliveries":           moveCarry("the person's notification inbox"),
	// Links to other services, made by the person.
	"history_import_user_mappings": moveColumns(moveCarry("which external user's history is imported into the person"), "silo_user_id", "silo_profile_id"),
	"backup_restored_profiles":     moveCarry("which backup profile a restore made this profile from"),

	// The old account's sign-ins and devices as the profile. The person signs
	// in with their own login from now on.
	"auth_sessions":                {Kind: profileMoveEnd, UserColumn: "user_id", ProfileColumn: "bound_profile_id", Revoke: true, Reason: "sign-ins of the old account bound to the profile"},
	"abs_sessions":                 {Kind: profileMoveEnd, UserColumn: "user_id", ProfileColumn: "profile_id", Revoke: true, Reason: "Audiobookshelf-compatible sign-ins of the old account as the profile"},
	"abs_playback_sessions":        moveEnd("Audiobookshelf-compatible playback of the old account's sign-ins"),
	"jellycompat_sessions":         moveColumns(moveEnd("Jellyfin-compatible sign-ins of the old account as the profile (deleted, as a profile deletion does)"), "streamapp_user_id", "profile_id"),
	"api_keys":                     moveEnd("the old account's API keys bound to the profile (a key's owner cannot change)"),
	"push_devices":                 moveEnd("push registrations of the old account's devices"),
	"android_push_installations":   moveEnd("push registrations of the old account's devices"),
	"apple_push_installations":     moveEnd("push registrations of the old account's devices"),
	"web_push_subscriptions":       moveEnd("browser push registrations of the old account's sign-ins"),
	"user_devices":                 moveEnd("download libraries of the old account's devices (their downloads and subscriptions go with them)"),
	"user_downloads":               moveEnd("downloads of the old account's devices"),
	"downloads":                    moveEnd("downloads of the old account's devices"),
	"download_subscriptions":       moveEnd("download subscriptions of the old account's devices"),
	"user_device_settings":         moveEnd("settings of the old account's devices"),
	"remote_device_capabilities":   moveEnd("remote-control state of the old account's devices"),
	"remote_commands":              moveColumns(moveEnd("remote-control commands to the old account's devices"), "target_user_id", "target_profile_id"),
	"progress_bootstrap_snapshots": moveEnd("sync snapshots of the old account's devices"),
	"playback_shuffles":            moveEnd("shuffle queues of the old account's sessions"),
	"playback_stop_commits":        moveEnd("stop receipts of the old account's sessions"),
	"device_login_requests":        moveColumns(moveEnd("device sign-ins approved by the old account as the profile"), "approved_by_user_id", "approved_profile_id"),
	"history_import_runs":          moveEnd("import runs into the old account (a run's target cannot change; the imported history itself is carried)"),
	// Their tokens are encrypted for the account and profile they were made
	// under (watchsync.TokenAAD, authStateAAD), so they cannot be read under
	// another account. The history they brought in is carried; the person
	// links the service again from the login.
	"watch_provider_connections":   moveEnd("watch-service links, whose tokens are bound to the old account and profile"),
	"watch_provider_auth_sessions": moveEnd("watch-service links being made, bound to the old account and profile"),
	"webhook_sync_profile_mappings": {Kind: profileMoveEnd, UserColumn: "", ProfileColumn: "silo_profile_id",
		Reason: "the old account's webhook sync no longer maps to the profile (deleted by the webhook step)"},

	// Records of what happened under the old account.
	"admin_playback_history":                moveKeep("what the old account played, for administrators"),
	"policy_decisions":                      moveKeep("the access decisions made for the old account"),
	"client_diagnostic_reports":             moveKeep("diagnostic reports sent by the old account's devices"),
	"media_request_events":                  moveColumns(moveKeep("who did what to a request, at the time"), "actor_user_id", "actor_profile_id"),
	"download_change_events":                moveKeep("the change feed of the old account's devices; it records the removals"),
	"playback_capacity_reservations":        moveColumns(moveKeep("live stream reservations of the old account, released by session or expiry"), "account_id", "profile_id"),
	"playback_sessions_sync":                moveKeep("playback sessions of the old account's devices"),
	"playback_session_lifecycle":            moveKeep("the old account's playback log"),
	"playback_v3_attempts":                  moveKeep("the old account's playback attempts"),
	"playback_route_events":                 moveKeep("the old account's playback routing log"),
	"livetv_sessions":                       moveKeep("the old account's live TV sessions"),
	"watch_together_rooms":                  moveColumns(moveKeep("watch-together rooms, which end on their own"), "host_user_id", "host_profile_id"),
	"watch_together_room_creation_receipts": moveColumns(moveKeep("watch-together receipts"), "host_user_id", "host_profile_id"),
	"watch_together_suggestions":            moveColumns(moveKeep("suggestions in a watch-together room"), "suggester_user_id", "suggester_profile_id"),
	"watch_together_suggestion_receipts":    moveKeep("watch-together receipts"),
	"watch_together_votes":                  moveColumns(moveKeep("votes in a watch-together room"), "voter_user_id", "voter_profile_id"),

	"notification_inbox_clocks": {Kind: profileMoveFollows, ProfileColumn: "profile_id", Reason: "keyed by the profile id alone"},
}

// bloemProfileMoveSteps are the tables a move handles in steps of its own.
var bloemProfileMoveSteps = map[string]string{
	"user_profiles":                            "copied under the target account, then deleted under the old one",
	"user_personal_collections":                "the profile's own collections are copied under the target account, then deleted under the old one",
	"user_personal_collection_items":           "the moved collections' items change account",
	"user_personal_collection_poster_variants": "the moved collections' posters change account",
	"user_personal_collection_smart_collages":  "the moved collections' collage records are dropped: a cache the next list read rebuilds, which a repoint cannot carry (its foreign key to the poster variants does not follow an account change)",
	"user_collection_groups":                   "a moved collection's shelf group is found by slug or label in the target account, or copied there",
	"user_settings":                            "the profile's section overrides move between the accounts' values, and its Jellyfin-client configuration (bloemJellyfinConfigurationKey) changes account",
	"jellycompat_displayprefs":                 "Jellyfin-compatible display preferences keyed by the profile's prefix change account",
	"webhook_sync_connections":                 "a connection of the old account whose default is the profile defaults to the old account's primary profile",
}

// moveCollectionDependants are keyed by (account, collection) and follow a
// moved collection.
var moveCollectionDependants = []string{
	"user_personal_collection_items",
	"user_personal_collection_poster_variants",
}

// MoveProfileInTx moves profile profileID from account from to account to in
// the caller's transaction, keeping its id; primary is the moved profile's
// primary flag. It judges no primaries: callers decide which profile may
// move. Every profile it adds passes the target account's profile limit.
func MoveProfileInTx(ctx context.Context, tx pgx.Tx, from, to int, profileID string, primary bool) error {
	if from == to {
		return fmt.Errorf("profile move: account %d to itself", from)
	}
	// The account locks first, in id order, as imports and profile deletion
	// take them; then the profile's history lock; then the collections.
	if err := LockAccountForImport(ctx, tx, min(from, to)); err != nil {
		return err
	}
	if err := LockAccountForImport(ctx, tx, max(from, to)); err != nil {
		return err
	}
	if err := userstore.LockImportedHistory(ctx, tx, from, profileID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM user_personal_collections
		WHERE user_id = $1 AND (profile_id = $2 OR id IN (
			SELECT collection_id FROM user_personal_collection_profiles WHERE user_id = $1 AND profile_id = $2))
		ORDER BY id FOR UPDATE`, from, profileID); err != nil {
		return fmt.Errorf("profile move: locking collections: %w", err)
	}
	var found int
	err := tx.QueryRow(ctx, `SELECT 1 FROM user_profiles WHERE user_id = $1 AND id = $2 FOR UPDATE`, from, profileID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProfileMoveNotFound
	}
	if err != nil {
		return fmt.Errorf("profile move: locking the profile: %w", err)
	}

	if err := copyRowsInTx(ctx, tx, "user_profiles", map[string]string{"user_id": "$3::integer", "is_primary": "$4::boolean"},
		"user_id = $1 AND id = $2", from, profileID, to, primary); err != nil {
		return profileCopyError(err)
	}
	collections, err := moveCollections(ctx, tx, from, to, profileID)
	if err != nil {
		return err
	}
	if err := dropCrossAccountLinks(ctx, tx, from, profileID, collections); err != nil {
		return err
	}
	for _, table := range sortedMoveTables(profileMoveCarry) {
		rule := bloemProfileMoveTables[table]
		// A row already under the target for this profile is a stray (the
		// target had no such profile), so the moved row replaces it.
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND %s = $2`,
			moveIdent(table), moveIdent(rule.UserColumn), moveIdent(rule.ProfileColumn)), to, profileID); err != nil {
			return fmt.Errorf("profile move: clearing %s: %w", table, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = $3 WHERE %s = $1 AND %s = $2`,
			moveIdent(table), moveIdent(rule.UserColumn), moveIdent(rule.UserColumn), moveIdent(rule.ProfileColumn)), from, profileID, to); err != nil {
			return fmt.Errorf("profile move: moving %s: %w", table, err)
		}
	}
	if err := moveSectionOverrides(ctx, tx, from, to, profileID); err != nil {
		return err
	}
	prefix := userstore.ProfileDisplayPrefsPrefix(profileID)
	if _, err := tx.Exec(ctx, `DELETE FROM jellycompat_displayprefs WHERE user_id = $1 AND left(prefs_id, length($2)) = $2`, to, prefix); err != nil {
		return fmt.Errorf("profile move: clearing display preferences: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE jellycompat_displayprefs SET user_id = $3 WHERE user_id = $1 AND left(prefs_id, length($2)) = $2`, from, prefix, to); err != nil {
		return fmt.Errorf("profile move: moving display preferences: %w", err)
	}
	configuration := bloemJellyfinConfigurationKey(profileID)
	if _, err := tx.Exec(ctx, `DELETE FROM user_settings WHERE user_id = $1 AND key = $2`, to, configuration); err != nil {
		return fmt.Errorf("profile move: clearing the Jellyfin configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE user_settings SET user_id = $3 WHERE user_id = $1 AND key = $2`, from, configuration, to); err != nil {
		return fmt.Errorf("profile move: moving the Jellyfin configuration: %w", err)
	}
	if err := endOldSignIns(ctx, tx, from, profileID); err != nil {
		return err
	}
	if len(collections) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM user_personal_collections WHERE user_id = $1 AND id = ANY($2)`, from, collections); err != nil {
			return fmt.Errorf("profile move: removing the old collections: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_profiles WHERE user_id = $1 AND id = $2`, from, profileID); err != nil {
		return fmt.Errorf("profile move: removing the old profile: %w", err)
	}
	return nil
}

// endOldSignIns ends the old account's sign-ins and devices as the profile,
// and points its webhook sync away from the profile.
func endOldSignIns(ctx context.Context, tx pgx.Tx, from int, profileID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE webhook_sync_connections SET default_profile_id = primary_profile.id
		FROM (SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary AND id <> $2) AS primary_profile
		WHERE webhook_sync_connections.user_id = $1 AND webhook_sync_connections.default_profile_id = $2`, from, profileID); err != nil {
		return fmt.Errorf("profile move: webhook sync default: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM webhook_sync_profile_mappings
		WHERE silo_profile_id = $2 AND connection_id IN (SELECT id FROM webhook_sync_connections WHERE user_id = $1)`, from, profileID); err != nil {
		return fmt.Errorf("profile move: webhook sync mappings: %w", err)
	}
	for _, table := range sortedMoveTables(profileMoveEnd) {
		rule := bloemProfileMoveTables[table]
		if rule.UserColumn == "" {
			continue // ended above
		}
		statement := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND %s = $2`, moveIdent(table), moveIdent(rule.UserColumn), moveIdent(rule.ProfileColumn))
		if rule.Revoke {
			statement = fmt.Sprintf(`UPDATE %s SET revoked_at = now() WHERE %s = $1 AND %s = $2 AND revoked_at IS NULL`,
				moveIdent(table), moveIdent(rule.UserColumn), moveIdent(rule.ProfileColumn))
		}
		if _, err := tx.Exec(ctx, statement, from, profileID); err != nil {
			return fmt.Errorf("profile move: ending %s: %w", table, err)
		}
	}
	return nil
}

// moveCollections copies the profile's own collections, and the shelf groups
// they sit in, under the target account, and moves the rows that hang off
// them. It returns the moved collections' ids; the old rows are deleted last.
func moveCollections(ctx context.Context, tx pgx.Tx, from, to int, profileID string) ([]string, error) {
	type owned struct {
		id    string
		group *string
	}
	rows, err := tx.Query(ctx, `SELECT id, group_id FROM user_personal_collections WHERE user_id = $1 AND profile_id = $2 ORDER BY id`, from, profileID)
	if err != nil {
		return nil, fmt.Errorf("profile move: reading collections: %w", err)
	}
	collections, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (owned, error) {
		var c owned
		return c, row.Scan(&c.id, &c.group)
	})
	if err != nil || len(collections) == 0 {
		return nil, err
	}
	ids := make([]string, len(collections))
	groups := map[string]string{}
	for i, c := range collections {
		ids[i] = c.id
		if c.group == nil {
			continue
		}
		if _, done := groups[*c.group]; done {
			continue
		}
		target, err := targetShelfGroup(ctx, tx, from, to, *c.group)
		if err != nil {
			return nil, err
		}
		groups[*c.group] = target
	}
	if err := copyRowsInTx(ctx, tx, "user_personal_collections", map[string]string{"user_id": "$3::integer", "group_id": "NULL"},
		"user_id = $1 AND id = ANY($2)", from, ids, to); err != nil {
		return nil, fmt.Errorf("profile move: copying collections: %w", err)
	}
	for _, c := range collections {
		if c.group == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE user_personal_collections SET group_id = $3 WHERE user_id = $1 AND id = $2`, to, c.id, groups[*c.group]); err != nil {
			return nil, fmt.Errorf("profile move: shelving a collection: %w", err)
		}
	}
	// A smart collage record points at a poster variant by (account,
	// collection, variant), and that key cannot change while the record
	// holds it. The record is a cache the next list read rebuilds.
	if _, err := tx.Exec(ctx, `DELETE FROM user_personal_collection_smart_collages WHERE user_id = ANY($1) AND collection_id = ANY($2)`,
		[]int{from, to}, ids); err != nil {
		return nil, fmt.Errorf("profile move: dropping smart collages: %w", err)
	}
	for _, table := range moveCollectionDependants {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id = $1 AND collection_id = ANY($2)`, moveIdent(table)), to, ids); err != nil {
			return nil, fmt.Errorf("profile move: clearing %s: %w", table, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET user_id = $3 WHERE user_id = $1 AND collection_id = ANY($2)`, moveIdent(table)), from, ids, to); err != nil {
			return nil, fmt.Errorf("profile move: moving %s: %w", table, err)
		}
	}
	return ids, nil
}

// targetShelfGroup is the target account's shelf group for one of the old
// account's: the one with its slug, else its label, else a copy (with the
// same id unless the target holds it).
func targetShelfGroup(ctx context.Context, tx pgx.Tx, from, to int, groupID string) (string, error) {
	var label, slug string
	if err := tx.QueryRow(ctx, `SELECT label, slug FROM user_collection_groups WHERE user_id = $1 AND id = $2`, from, groupID).Scan(&label, &slug); err != nil {
		return "", fmt.Errorf("profile move: reading a shelf group: %w", err)
	}
	var existing string
	err := tx.QueryRow(ctx, `SELECT id FROM user_collection_groups WHERE user_id = $1 AND (slug = $2 OR label = $3)
		ORDER BY (slug = $2) DESC LIMIT 1`, to, slug, label).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("profile move: finding a shelf group: %w", err)
	}
	target := groupID
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_collection_groups WHERE user_id = $1 AND id = $2)`, to, groupID).Scan(&taken); err != nil {
		return "", fmt.Errorf("profile move: checking a shelf group: %w", err)
	}
	if taken {
		target = generateUUID()
	}
	if err := copyRowsInTx(ctx, tx, "user_collection_groups", map[string]string{"user_id": "$3::integer", "id": "$4::text"},
		"user_id = $1 AND id = $2", from, groupID, to, target); err != nil {
		return "", fmt.Errorf("profile move: copying a shelf group: %w", err)
	}
	return target, nil
}

// dropCrossAccountLinks removes what joins the moving profile to the
// collections that stay, and the profiles that stay to the collections that
// move: sharing, allowances and sort orders cannot cross accounts.
func dropCrossAccountLinks(ctx context.Context, tx pgx.Tx, from int, profileID string, moved []string) error {
	if moved == nil {
		moved = []string{}
	}
	for _, statement := range []string{
		`DELETE FROM user_personal_collection_profiles WHERE user_id = $1
			AND ((collection_id = ANY($3) AND profile_id <> $2) OR (profile_id = $2 AND NOT (collection_id = ANY($3))))`,
		`DELETE FROM profile_collection_allowances WHERE user_id = $1
			AND ((collection_id = ANY($3) AND profile_id <> $2) OR (profile_id = $2 AND NOT (collection_id = ANY($3))))`,
		`DELETE FROM user_collection_sort_preferences WHERE user_id = $1 AND collection_kind = 'user'
			AND ((collection_id = ANY($3) AND profile_id <> $2) OR (profile_id = $2 AND NOT (collection_id = ANY($3))))`,
	} {
		if _, err := tx.Exec(ctx, statement, from, profileID, moved); err != nil {
			return fmt.Errorf("profile move: unsharing collections: %w", err)
		}
	}
	return nil
}

// moveSectionOverrides moves the profile's entries in the accounts'
// section-override values (one JSON value per scope and library holds every
// profile's overrides). The account locks taken first cover both accounts'
// section-override locks.
func moveSectionOverrides(ctx context.Context, tx pgx.Tx, from, to int, profileID string) error {
	read := func(userID int) (map[string][]userstore.SectionOverride, error) {
		rows, err := tx.Query(ctx, `SELECT key, value FROM user_settings WHERE user_id = $1 AND key LIKE 'section\_overrides:%' FOR UPDATE`, userID)
		if err != nil {
			return nil, fmt.Errorf("profile move: reading section overrides: %w", err)
		}
		values := map[string][]userstore.SectionOverride{}
		var key, value string
		if _, err := pgx.ForEachRow(rows, []any{&key, &value}, func() error {
			var overrides []userstore.SectionOverride
			if err := json.Unmarshal([]byte(value), &overrides); err != nil {
				return fmt.Errorf("profile move: decoding section overrides %s: %w", key, err)
			}
			values[key] = overrides
			return nil
		}); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("profile move: reading section overrides: %w", err)
		}
		return values, nil
	}
	write := func(userID int, key string, overrides []userstore.SectionOverride) error {
		var err error
		if len(overrides) == 0 {
			_, err = tx.Exec(ctx, `DELETE FROM user_settings WHERE user_id = $1 AND key = $2`, userID, key)
		} else {
			data, _ := json.Marshal(overrides)
			_, err = tx.Exec(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES ($1, $2, $3)
				ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value`, userID, key, string(data))
		}
		if err != nil {
			return fmt.Errorf("profile move: writing section overrides %s: %w", key, err)
		}
		return nil
	}
	source, err := read(from)
	if err != nil {
		return err
	}
	target, err := read(to)
	if err != nil {
		return err
	}
	isProfile := func(o userstore.SectionOverride) bool { return o.ProfileID == profileID }
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		overrides := source[key]
		var moving []userstore.SectionOverride
		for _, o := range overrides {
			if isProfile(o) {
				moving = append(moving, o)
			}
		}
		if len(moving) == 0 {
			continue
		}
		if err := write(from, key, slices.DeleteFunc(slices.Clone(overrides), isProfile)); err != nil {
			return err
		}
		kept := slices.DeleteFunc(slices.Clone(target[key]), isProfile)
		if err := write(to, key, append(kept, moving...)); err != nil {
			return err
		}
	}
	return nil
}

// copyRowsInTx inserts copies of table's rows matching where, every column
// as it is except those in set, which take the given SQL expressions. The
// column list is read from the catalog, so a column added later is copied
// too.
func copyRowsInTx(ctx context.Context, tx pgx.Tx, table string, set map[string]string, where string, args ...any) error {
	rows, err := tx.Query(ctx, `SELECT attname FROM pg_attribute WHERE attrelid = $1::regclass AND attnum > 0
		AND NOT attisdropped AND attgenerated = '' ORDER BY attnum`, "public."+table)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	selected := make([]string, len(names))
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = moveIdent(name)
		selected[i] = quoted[i]
		if expression, ok := set[name]; ok {
			selected[i] = expression
		}
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE SELECT %s FROM %s WHERE %s`,
		moveIdent(table), strings.Join(quoted, ", "), strings.Join(selected, ", "), moveIdent(table), where), args...)
	return err
}

// profileCopyError names the target account's refusals of a profile copy.
func profileCopyError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23514" && pgErr.ConstraintName == "user_profiles_entitlement_limit":
			return ErrProfileMoveLimit
		case pgErr.Code == "23505":
			return ErrProfileMoveConflict
		}
	}
	return fmt.Errorf("profile move: copying the profile: %w", err)
}

func sortedMoveTables(kind profileMoveKind) []string {
	var tables []string
	for table, rule := range bloemProfileMoveTables {
		if rule.Kind == kind {
			tables = append(tables, table)
		}
	}
	slices.Sort(tables)
	return tables
}

func moveIdent(name string) string { return pgx.Identifier{name}.Sanitize() }

// bloemJellyfinConfigurationKey is the account setting holding one profile's
// Jellyfin-client configuration (the key internal/jellycompat writes,
// configurationKey).
func bloemJellyfinConfigurationKey(profileID string) string {
	return "jellycompat:configuration:" + profileID
}
