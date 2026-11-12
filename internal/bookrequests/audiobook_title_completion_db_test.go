package bookrequests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// nativeAudiobookEnv binds the storage plugin's audiobook capability to its
// own native library, as the setup does for Bookwarehouse's abiplayer route.
func nativeAudiobookEnv(t *testing.T) (*audiobookEnv, nativeStorageLibrary, nativeStorageLibrary) {
	t.Helper()
	_, v, pool := databaseFixture(t)
	env := &audiobookEnv{v: v, pool: pool, fake: &audiobookFake{}, notices: &audiobookNoticeSpy{}}
	env.s = NewService(pool, env.notices)
	env.s.SetAudiobookFulfillment(env.fake)
	env.admin = v
	env.admin.Admin = true
	audio := nativeStorageLibraryFixture(t, pool, "audiobooks", "Audio books")
	other := nativeStorageLibraryFixture(t, pool, "audiobooks", "Other audio")
	id := audio.requestIntegration(t, pool, AudiobookStorageCapabilityID, "audiobook")
	if _, err := env.s.SetAudiobookIntegrationLibrary(t.Context(), env.admin, id, audio.folder, nil); err != nil {
		t.Fatal(err)
	}
	env.integration, env.folder = id, audio.folder
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		out := handedOff(in, "abb:/abss/requested-book/")
		out.Receipt = abiplayerReceipt(in)
		out.ProviderStatus, out.ExternalStatus = "downloading", "downloading"
		return out, nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	return env, audio, other
}

// abiplayerReceipt is the storage plugin's receipt for a request placed with
// Bookwarehouse's abiplayer route: kind qbittorrent, the abiplayer request id
// in torrent_id, and the normalized title and author it asked for.
func abiplayerReceipt(in AudiobookDispatchInput) Receipt {
	raw, _ := json.Marshal(AudiobookReceipt{Kind: "qbittorrent", ID: "cccccccccccccccccccccccccccccccccccccccc", RequestID: in.SubmissionID, Attempt: in.Attempt, Client: "0123456789abcdef", Confirmed: true, Sel: "Requested Book by Requested Author", TorrentID: "4711", NormTitle: "requested book", NormAuthor: "requested author"})
	return Receipt{ExternalID: "abr1." + base64.RawURLEncoding.EncodeToString(raw), Status: "downloading"}
}

// publishBook publishes a native audiobook with its title and credited
// authors, under the Bookwarehouse audiobook id "book-<content>".
func (n nativeStorageLibrary) publishBook(t *testing.T, pool *pgxpool.Pool, content, title string, authors ...string) {
	t.Helper()
	n.publishBookAt(t, pool, content, title, 1, authors...)
}

func (n nativeStorageLibrary) publishBookAt(t *testing.T, pool *pgxpool.Pool, content, title string, configurationRevision int64, authors ...string) {
	t.Helper()
	bookSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES($1,'audiobook',$2)`, content, title)
	for i, author := range authors {
		bookSQL(t, pool, `INSERT INTO people(id,name) SELECT COALESCE(max(id),0)+1,$1 FROM people`, author)
		bookSQL(t, pool, `INSERT INTO item_people(id,content_id,person_id,kind,sort_order) SELECT COALESCE((SELECT max(id) FROM item_people),0)+1,$1,(SELECT max(id) FROM people),7,$2`, content, i)
	}
	n.publishTrack(t, pool, content, "book-"+content, 1, configurationRevision)
}

// A request whose ISBN matches nothing in the ASIN-only library completes
// from the one new library item with its title and author. An item already
// in the library before the request, another author's book of that title, a
// stale storage reference and another library's copy do not complete it, and
// neither does the plugin reporting the download complete with a
// Bookwarehouse audiobook the library does not have yet.
func TestAudiobookNativeCompletionByTitleDatabase(t *testing.T) {
	env, audio, other := nativeAudiobookEnv(t)
	audio.publishBook(t, env.pool, "owned-copy", "Project Hail Mary", "Andy Weir")
	bookSQL(t, env.pool, `UPDATE media_items SET created_at=now()-interval '1 day' WHERE content_id='owned-copy'`)

	r := env.approve(t, CreateInput{Title: "Project Hail Mary: A Novel", Author: "Andy Weir", ISBN: "9780306406157"})
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("an item older than the request completed it: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "approved" || got.Fulfilment != "downloading" {
		t.Fatalf("handed-off state %+v", got)
	}

	audio.publishBook(t, env.pool, "other-author", "Project Hail Mary", "Someone Else")
	audio.publishBook(t, env.pool, "other-title", "Artemis", "Andy Weir")
	audio.publishBook(t, env.pool, "sequel-title", "Project Hail Mary Returns", "Andy Weir")
	audio.publishBookAt(t, env.pool, "stale-copy", "Project Hail Mary", 99, "Andy Weir")
	other.publishBook(t, env.pool, "foreign-copy", "Project Hail Mary", "Andy Weir")
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("a non-matching item completed the request: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "approved" || got.Fulfilment != "downloading" {
		t.Fatalf("still downloading %+v", got)
	}

	// The plugin's word alone does not complete it.
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified", AudiobookID: "not-scanned-yet"}), nil
	}
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("completed on the plugin's word: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "approved" || got.Fulfilment != "awaiting_library" {
		t.Fatalf("verified download state %+v", got)
	}

	audio.publishBook(t, env.pool, "new-copy", "PROJECT HAIL MARY", "Andy Weir.")
	bookSQL(t, env.pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('new-copy','audiobook','asin','B08FHBV4ZX')`)
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("the new library item did not complete the request: %d", n)
	}
	got := env.get(t, r.ID)
	if got.Status != "completed" || got.Fulfilment != "library" || got.LibraryContentID != "new-copy" {
		t.Fatalf("completion %+v", got)
	}
	if fulfills, checks, _ := env.fake.counts(); fulfills != 1 || checks != 2 {
		t.Fatalf("provider calls: fulfill %d check %d", fulfills, checks)
	}
}

// While the download is still running, a match on the next pass completes the
// request without the plugin reporting it finished.
func TestAudiobookNativeTitleMatchWhileDownloadingDatabase(t *testing.T) {
	env, audio, _ := nativeAudiobookEnv(t)
	r := env.approve(t, CreateInput{Title: "Les Miserables", Author: "Victor Hugo & Isabel Hapgood", ISBN: "9780306406157"})
	env.reconcile(t)
	audio.publishBook(t, env.pool, "hugo", "Les Misérables - Unabridged", "Isabel Hapgood", "Víctor Hugo")
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("matching item did not complete a downloading request: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "completed" || got.LibraryContentID != "hugo" {
		t.Fatalf("completion %+v", got)
	}
}

// Two new items with the request's title and author go to an administrator.
func TestAudiobookNativeTitleMatchesGoToReviewDatabase(t *testing.T) {
	env, audio, _ := nativeAudiobookEnv(t)
	r := env.approve(t, CreateInput{Title: "Good Omens", Author: "Neil Gaiman", ISBN: "9780306406157"})
	env.reconcile(t)
	audio.publishBook(t, env.pool, "omens-a", "Good Omens", "Terry Pratchett", "Neil Gaiman")
	audio.publishBook(t, env.pool, "omens-b", "Good Omens: The Nice and Accurate Prophecies", "Neil Gaiman")
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("an ambiguous title match completed the request: %d", n)
	}
	got := env.get(t, r.ID)
	if got.Status != "approved" || got.Fulfilment != "review" {
		t.Fatalf("ambiguous match state %+v", got)
	}
	row := env.row(t, r.ID)
	if row.providerStatus != "review" || row.reviewKind != "verification" {
		t.Fatalf("ambiguous match dispatch %+v", row)
	}
	var issues int
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_audiobook_request_decisions WHERE request_id=$1 AND phase='verify' AND outcome='review' AND envelope->'issues' ? 'multiple_items'`, r.ID).Scan(&issues); err != nil || issues != 1 {
		t.Fatalf("multiple-items review decisions %d %v", issues, err)
	}
	if notices := env.notices.attentions(); len(notices) != 1 {
		t.Fatalf("review attention %+v", notices)
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if _, checks, _ := env.fake.counts(); checks != 1 {
		t.Fatalf("a request in review was checked again: %d", checks)
	}
}

// An identifier match wins over title matches: the request completes from
// the item carrying its ASIN, even though two items share its title.
func TestAudiobookNativeIdentifierBeforeTitleDatabase(t *testing.T) {
	env, audio, _ := nativeAudiobookEnv(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Fixture Author", ASIN: "B0SYNTHET1"})
	env.reconcile(t)
	audio.publishBook(t, env.pool, "saga-a", "Synthetic Saga", "Fixture Author")
	audio.publishBook(t, env.pool, "saga-b", "Synthetic Saga", "Fixture Author")
	audio.publishBook(t, env.pool, "saga-asin", "Synthetic Saga (Dramatized)", "Fixture Author")
	bookSQL(t, env.pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('saga-asin','audiobook','asin','B0SYNTHET1')`)
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("identifier match did not complete the request: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "completed" || got.LibraryContentID != "saga-asin" {
		t.Fatalf("completion %+v", got)
	}
}

// A download the plugin holds for review is not completed by a title match:
// the plugin found several Bookwarehouse audiobooks, and an administrator
// decides.
func TestAudiobookNativeTitleMatchRespectsPluginReviewDatabase(t *testing.T) {
	env, audio, _ := nativeAudiobookEnv(t)
	r := env.approve(t, CreateInput{Title: "The Hobbit", Author: "J. R. R. Tolkien", ISBN: "9780306406157"})
	env.reconcile(t)
	audio.publishBook(t, env.pool, "hobbit", "The Hobbit", "J.R.R. Tolkien")
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("review", "review", &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "review", Issues: []string{"multiple_items"}}), nil
	}
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("a title match completed a request held for review: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "approved" || got.Fulfilment != "review" {
		t.Fatalf("review state %+v", got)
	}
}
