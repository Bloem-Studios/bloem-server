package pgstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// moveFixture is a household of two accounts: a main account with its
// primary profile, the profile that moves (Sam) and one that stays (Alex),
// and a second account the profile moves to.
type moveFixture struct {
	t                  *testing.T
	pool               *pgxpool.Pool
	main, login        int
	primary, sam, alex string
}

func newMoveFixture(t *testing.T) *moveFixture {
	t.Helper()
	pool := testPool(t)
	f := &moveFixture{t: t, pool: pool, main: seedTestUser(t, pool, "move-main"), login: seedTestUser(t, pool, "move-login")}
	f.primary = f.profile(f.main, "Main")
	f.sam = f.profile(f.main, "Sam")
	f.alex = f.profile(f.main, "Alex")
	return f
}

func (f *moveFixture) profile(userID int, name string) string {
	f.t.Helper()
	id := NewProfileID()
	if err := newStore(f.pool, userID).CreateProfile(context.Background(), userstore.Profile{ID: id, Name: name}); err != nil {
		f.t.Fatalf("create profile %s: %v", name, err)
	}
	return id
}

func (f *moveFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *moveFixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// move runs MoveProfileInTx in a transaction of its own and commits it.
func (f *moveFixture) move(from, to int, profileID string, primary bool) error {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := MoveProfileInTx(ctx, tx, from, to, profileID, primary); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (f *moveFixture) mustMove(from, to int, profileID string, primary bool) {
	f.t.Helper()
	if err := f.move(from, to, profileID, primary); err != nil {
		f.t.Fatalf("move profile: %v", err)
	}
}

// collection gives a profile a personal collection with one item, in the
// account's shelf group with slug, and returns its id.
func (f *moveFixture) collection(userID int, profileID, name, groupSlug string) string {
	f.t.Helper()
	id := NewProfileID()
	var groupID any
	if groupSlug != "" {
		var existing string
		err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_collection_groups WHERE user_id = $1 AND slug = $2`, userID, groupSlug).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			existing = NewProfileID()
			f.exec(`INSERT INTO user_collection_groups (user_id, id, label, title, name, slug) VALUES ($1, $2, $3, $3, $3, $4)`,
				userID, existing, "Shelf "+groupSlug, groupSlug)
		} else if err != nil {
			f.t.Fatal(err)
		}
		groupID = existing
	}
	f.exec(`INSERT INTO user_personal_collections (id, user_id, profile_id, creator_profile_id, name, group_id) VALUES ($1, $2, $3, $3, $4, $5)`,
		id, userID, profileID, name, groupID)
	f.exec(`INSERT INTO user_personal_collection_items (user_id, collection_id, media_item_id) VALUES ($1, $2, 'item-in-'||$2)`, userID, id)
	return id
}

func TestProfileMoveCoversEveryProfileTable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		  AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0
		              AND NOT a.attisdropped AND a.attname LIKE '%profile\_id')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 50 {
		t.Fatalf("found %d profile tables; the schema query is wrong", len(tables))
	}
	for _, table := range tables {
		_, ruled := bloemProfileMoveTables[table]
		_, stepped := bloemProfileMoveSteps[table]
		if !ruled && !stepped {
			t.Errorf("%s has a profile column but a profile move does not say what happens to it: add it to bloemProfileMoveTables (carry, end, keep or follows) with its reason", table)
		}
	}
	hasColumn := func(table, column string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`, table, column).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for table, rule := range bloemProfileMoveTables {
		if rule.Reason == "" {
			t.Errorf("%s has no reason", table)
		}
		if !hasColumn(table, rule.ProfileColumn) {
			t.Errorf("%s: no column %s", table, rule.ProfileColumn)
		}
		if rule.UserColumn != "" && !hasColumn(table, rule.UserColumn) {
			t.Errorf("%s: no column %s", table, rule.UserColumn)
		}
	}
	for table := range bloemProfileMoveSteps {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Errorf("step table %s does not exist (%v)", table, err)
		}
	}
}

func TestMoveCarriesThePersonsData(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	f.exec(`UPDATE user_profiles SET pin_hash = 'pin-hash-of-sam' WHERE user_id = $1 AND id = $2`, f.main, f.sam)
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 300)`, f.main, f.sam)
	f.exec(`INSERT INTO user_watch_history (id, user_id, profile_id, media_item_id) VALUES ($3, $1, $2, 'film-1')`, f.main, f.sam, NewProfileID())
	f.exec(`INSERT INTO user_watchlist (user_id, profile_id, media_item_id) VALUES ($1, $2, 'film-2')`, f.main, f.sam)
	f.exec(`INSERT INTO user_ratings (user_id, profile_id, media_item_id, rating) VALUES ($1, $2, 'film-1', 8)`, f.main, f.sam)
	f.exec(`INSERT INTO user_audio_preferences (user_id, profile_id, series_id) VALUES ($1, $2, 'series-1')`, f.main, f.sam)
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 60)`, f.main, f.alex)
	collection := f.collection(f.main, f.sam, "Sam's favourites", "family")
	mainStore := newStore(f.pool, f.main)
	if err := mainStore.SaveSectionOverrides(ctx, f.sam, "home", "", []userstore.SectionOverride{{SectionID: "default-recently-added", Hidden: true}}); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.SaveSectionOverrides(ctx, f.alex, "home", "", []userstore.SectionOverride{{SectionID: "default-continue-watching", Hidden: true}}); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO jellycompat_displayprefs (user_id, prefs_id, client, value) VALUES ($1, $2, 'emby', '{}')`,
		f.main, userstore.ProfileDisplayPrefsPrefix(f.sam)+"usersettings")

	f.mustMove(f.main, f.login, f.sam, true)

	for _, table := range []string{"user_watch_progress", "user_watch_history", "user_watchlist", "user_ratings", "user_audio_preferences"} {
		if got := f.count(fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1 AND profile_id = $2`, table), f.login, f.sam); got != 1 {
			t.Errorf("%s: %d rows of Sam's under the login; want 1", table, got)
		}
		if got := f.count(fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1 AND profile_id = $2`, table), f.main, f.sam); got != 0 {
			t.Errorf("%s: %d rows of Sam's left under the main account", table, got)
		}
	}
	var pin string
	var primary bool
	if err := f.pool.QueryRow(ctx, `SELECT pin_hash, is_primary FROM user_profiles WHERE user_id = $1 AND id = $2`, f.login, f.sam).Scan(&pin, &primary); err != nil {
		t.Fatalf("Sam's profile under the login: %v", err)
	}
	if pin != "pin-hash-of-sam" || !primary {
		t.Errorf("Sam's profile under the login: pin %q primary %v; want the same PIN, primary", pin, primary)
	}
	if got := f.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1`, f.main); got != 2 {
		t.Errorf("main account has %d profiles; want its primary and Alex", got)
	}
	if got := f.count(`SELECT count(*) FROM user_watch_progress WHERE user_id = $1 AND profile_id = $2`, f.main, f.alex); got != 1 {
		t.Errorf("Alex's progress: %d rows; want it untouched", got)
	}
	var groupSlug string
	if err := f.pool.QueryRow(ctx, `SELECT g.slug FROM user_personal_collections c JOIN user_collection_groups g ON g.user_id = c.user_id AND g.id = c.group_id
		WHERE c.user_id = $1 AND c.id = $2`, f.login, collection).Scan(&groupSlug); err != nil || groupSlug != "family" {
		t.Errorf("Sam's collection under the login in shelf %q (%v); want family", groupSlug, err)
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collection_items WHERE user_id = $1 AND collection_id = $2`, f.login, collection); got != 1 {
		t.Errorf("collection items under the login: %d; want 1", got)
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collections WHERE user_id = $1 AND id = $2`, f.main, collection); got != 0 {
		t.Errorf("Sam's collection is still the main account's too")
	}
	if got, err := newStore(f.pool, f.login).ListSectionOverrides(ctx, f.sam, "home", ""); err != nil || len(got) != 1 {
		t.Errorf("Sam's home overrides under the login: %v, %v; want one", got, err)
	}
	if got, err := mainStore.ListSectionOverrides(ctx, f.sam, "home", ""); err != nil || len(got) != 0 {
		t.Errorf("Sam's home overrides left under the main account: %v, %v", got, err)
	}
	if got, err := mainStore.ListSectionOverrides(ctx, f.alex, "home", ""); err != nil || len(got) != 1 {
		t.Errorf("Alex's home overrides: %v, %v; want them untouched", got, err)
	}
	if got := f.count(`SELECT count(*) FROM jellycompat_displayprefs WHERE user_id = $1`, f.login); got != 1 {
		t.Errorf("display preferences under the login: %d; want 1", got)
	}
}

func TestMoveEndsTheOldAccountsSignIns(t *testing.T) {
	f := newMoveFixture(t)
	bound, unbound := NewProfileID(), NewProfileID()
	expires := time.Now().Add(time.Hour)
	f.exec(`INSERT INTO auth_sessions (id, user_id, expires_at, bound_profile_id, purpose, absolute_expires_at) VALUES ($1, $2, $3, $4, 'remote_playback', $3)`, bound, f.main, expires, f.sam)
	f.exec(`INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`, unbound, f.main, expires)
	f.exec(`INSERT INTO jellycompat_sessions (token, username, account_username, profile_id, profile_name, pseudo_user_id, streamapp_user_id,
		streamapp_access_token, streamapp_refresh_token, streamapp_token_expiry, expires_at)
		VALUES ($1, 'main#Sam', 'main', $2, 'Sam', gen_random_uuid(), $3, 'access', 'refresh', $4, $4)`, NewProfileID(), f.sam, f.main, expires)
	f.exec(`INSERT INTO user_devices (user_id, profile_id, device_id) VALUES ($1, $2, 'phone-1')`, f.main, f.sam)

	f.mustMove(f.main, f.login, f.sam, true)

	if got := f.count(`SELECT count(*) FROM auth_sessions WHERE id = $1 AND revoked_at IS NOT NULL`, bound); got != 1 {
		t.Errorf("the session bound to Sam is not revoked")
	}
	if got := f.count(`SELECT count(*) FROM auth_sessions WHERE id = $1 AND revoked_at IS NULL`, unbound); got != 1 {
		t.Errorf("the main account's other session was revoked")
	}
	if got := f.count(`SELECT count(*) FROM jellycompat_sessions WHERE profile_id = $1`, f.sam); got != 0 {
		t.Errorf("Sam's Jellyfin session as the main account is still there")
	}
	if got := f.count(`SELECT count(*) FROM user_devices WHERE profile_id = $1`, f.sam); got != 0 {
		t.Errorf("Sam's download device under the main account is still there")
	}
}

func TestMoveDropsSharingWithProfilesThatStay(t *testing.T) {
	f := newMoveFixture(t)
	sams := f.collection(f.main, f.sam, "Sam's", "")
	alexs := f.collection(f.main, f.alex, "Alex's", "")
	f.exec(`INSERT INTO user_personal_collection_profiles (user_id, collection_id, profile_id) VALUES ($1, $2, $3), ($1, $2, $4), ($1, $5, $3), ($1, $5, $4)`,
		f.main, sams, f.sam, f.alex, alexs)

	f.mustMove(f.main, f.login, f.sam, true)

	if got := f.count(`SELECT count(*) FROM user_personal_collection_profiles WHERE collection_id = $1 AND profile_id = $2`, sams, f.alex); got != 0 {
		t.Errorf("Sam's collection is still shared with Alex")
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collection_profiles WHERE user_id = $1 AND collection_id = $2 AND profile_id = $3`, f.login, sams, f.sam); got != 1 {
		t.Errorf("Sam lost the visibility row of their own collection")
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collection_profiles WHERE collection_id = $1 AND profile_id = $2`, alexs, f.sam); got != 0 {
		t.Errorf("Alex's collection is still shared with Sam")
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collections WHERE user_id = $1 AND id = $2`, f.main, alexs); got != 1 {
		t.Errorf("Alex's collection is no longer the main account's")
	}
}

func TestMoveReplacesStrayRowsUnderTheTarget(t *testing.T) {
	f := newMoveFixture(t)
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 300)`, f.main, f.sam)
	// A heartbeat that landed under the login after an earlier move back.
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 12)`, f.login, f.sam)

	f.mustMove(f.main, f.login, f.sam, true)

	var position float64
	if err := f.pool.QueryRow(context.Background(), `SELECT position_seconds FROM user_watch_progress WHERE user_id = $1 AND profile_id = $2 AND media_item_id = 'film-1'`,
		f.login, f.sam).Scan(&position); err != nil || position != 300 {
		t.Errorf("progress under the login: %v (%v); want the moved 300", position, err)
	}
}

func TestMoveRefusesAProfileOfAnotherAccount(t *testing.T) {
	f := newMoveFixture(t)
	other := f.profile(f.login, "Someone else")
	if err := f.move(f.main, f.login, other, true); !errors.Is(err, ErrProfileMoveNotFound) {
		t.Fatalf("move another account's profile: %v; want ErrProfileMoveNotFound", err)
	}
}

func TestMoveIntoAFullAccountChangesNothing(t *testing.T) {
	f := newMoveFixture(t)
	f.exec(`UPDATE users SET max_profiles = 1 WHERE id = $1`, f.login)
	f.profile(f.login, "Already here")
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 300)`, f.main, f.sam)

	if err := f.move(f.main, f.login, f.sam, true); !errors.Is(err, ErrProfileMoveLimit) {
		t.Fatalf("move into a full account: %v; want ErrProfileMoveLimit", err)
	}
	if got := f.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1 AND id = $2`, f.main, f.sam); got != 1 {
		t.Errorf("Sam is no longer the main account's")
	}
	if got := f.count(`SELECT count(*) FROM user_watch_progress WHERE user_id = $1 AND profile_id = $2`, f.main, f.sam); got != 1 {
		t.Errorf("Sam's progress left the main account")
	}
}

func TestMoveBackAndForthKeepsEverything(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	f.exec(`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds) VALUES ($1, $2, 'film-1', 300)`, f.main, f.sam)
	f.exec(`INSERT INTO user_ratings (user_id, profile_id, media_item_id, rating) VALUES ($1, $2, 'film-1', 8)`, f.main, f.sam)
	collection := f.collection(f.main, f.sam, "Sam's", "family")
	if err := newStore(f.pool, f.main).SaveSectionOverrides(ctx, f.sam, "home", "", []userstore.SectionOverride{{SectionID: "default-recently-added", Hidden: true}}); err != nil {
		t.Fatal(err)
	}

	f.mustMove(f.main, f.login, f.sam, true)
	f.mustMove(f.login, f.main, f.sam, false)

	var primary bool
	if err := f.pool.QueryRow(ctx, `SELECT is_primary FROM user_profiles WHERE user_id = $1 AND id = $2`, f.main, f.sam).Scan(&primary); err != nil || primary {
		t.Fatalf("Sam back on the main account: primary %v (%v); want an ordinary profile", primary, err)
	}
	for table, want := range map[string]int{"user_watch_progress": 1, "user_ratings": 1, "user_personal_collections": 1} {
		if got := f.count(fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1 AND profile_id = $2`, table), f.main, f.sam); got != want {
			t.Errorf("%s: %d of Sam's rows on the main account; want %d", table, got, want)
		}
	}
	if got := f.count(`SELECT count(*) FROM user_personal_collections c JOIN user_collection_groups g ON g.user_id = c.user_id AND g.id = c.group_id
		WHERE c.user_id = $1 AND c.id = $2 AND g.slug = 'family'`, f.main, collection); got != 1 {
		t.Errorf("Sam's collection is not back in the main account's family shelf")
	}
	if got := f.count(`SELECT count(*) FROM user_collection_groups WHERE user_id = $1`, f.main); got != 1 {
		t.Errorf("the main account has %d shelf groups; want the one it had", got)
	}
	if got, err := newStore(f.pool, f.main).ListSectionOverrides(ctx, f.sam, "home", ""); err != nil || len(got) != 1 {
		t.Errorf("Sam's overrides back on the main account: %v, %v", got, err)
	}
	if got := f.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1`, f.login); got != 0 {
		t.Errorf("the login still has %d profiles", got)
	}
}
