package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// searchMirrorKeyActions are the actions a dedicated mirror key needs: the spec's list plus
// indexes.get, which the copy's cleanup needs to list the mirror's indexes (ruling 8). The
// operator creates the key with exactly this list, on indexes "<prefix>*", and
// TestSearchMirrorContractKeyActionsAreEnough proves the list is enough on a real server.
var searchMirrorKeyActions = []string{"indexes.create", "indexes.get", "indexes.delete", "indexes.swap",
	"documents.add", "documents.get", "documents.delete", "settings.update", "tasks.get", "stats.get"}

const (
	// searchMirrorTestURLEnv names a real Meilisearch the contract cases also run against.
	searchMirrorTestURLEnv = "BLOEM_TEST_MEILISEARCH_URL"
	// searchMirrorTestMasterKeyEnv is that server's master key. The cases use it only to create
	// a limited key per case, to read settings and to clean up.
	searchMirrorTestMasterKeyEnv = "BLOEM_TEST_MEILISEARCH_MASTER_KEY"
	// searchMirrorFakeMasterKey is the fake's master key in the contract cases.
	searchMirrorFakeMasterKey   = "fake-contract-master-key"
	searchMirrorContractTimeout = 10 * time.Second
)

// searchMirrorContractServer is one server the contract cases run against.
type searchMirrorContractServer struct {
	name      string
	url       string
	masterKey string
	fake      *fakeMeili // nil on the real server
	skip      string     // why the cases skip this server; empty when they run
}

// searchMirrorContractServers returns the fake, plus a real server when BLOEM_TEST_MEILISEARCH_URL
// (and BLOEM_TEST_MEILISEARCH_MASTER_KEY) are set. On the real server every case runs with a key
// created for the case, limited to searchMirrorKeyActions on indexes "<prefix>*". The fake gets the
// same treatment, so both answer the same requests.
func searchMirrorContractServers(t *testing.T) []searchMirrorContractServer {
	t.Helper()
	fake := newFakeMeili(t)
	fake.RequireKey(searchMirrorFakeMasterKey)
	servers := []searchMirrorContractServer{{name: "fake", url: fake.URL(), masterKey: searchMirrorFakeMasterKey, fake: fake}}

	realServer := searchMirrorContractServer{name: "meilisearch"}
	rawURL := strings.TrimSpace(os.Getenv(searchMirrorTestURLEnv))
	masterKey := strings.TrimSpace(os.Getenv(searchMirrorTestMasterKeyEnv))
	switch {
	case rawURL == "":
		realServer.skip = searchMirrorTestURLEnv + " not set"
	case masterKey == "":
		t.Fatalf("%s is set but %s is not: the contract cases need the master key to create a limited key", searchMirrorTestURLEnv, searchMirrorTestMasterKeyEnv)
	default:
		realServer.url = rawURL
		realServer.masterKey = masterKey
	}
	return append(servers, realServer)
}

// searchMirrorContractCase is one case's view of one server.
type searchMirrorContractCase struct {
	server searchMirrorContractServer
	// prefix is unique to the case, so cases never share an index on a real server.
	prefix string
	// key is the limited key created for the case; client sends it.
	key    string
	client *meilisearchClient
	// master sends the master key. It is used only for checks the limited key may not make.
	master *meilisearchClient
}

// runSearchMirrorContract runs fn once per contract server, as a subtest named after the server.
func runSearchMirrorContract(t *testing.T, fn func(t *testing.T, c searchMirrorContractCase)) {
	t.Helper()
	for _, server := range searchMirrorContractServers(t) {
		t.Run(server.name, func(t *testing.T) {
			if server.skip != "" {
				t.Skip("SKIP: " + server.skip)
			}
			fn(t, newSearchMirrorContractCase(t, server))
		})
	}
}

func newSearchMirrorContractCase(t *testing.T, server searchMirrorContractServer) searchMirrorContractCase {
	t.Helper()
	ctx := context.Background()
	prefix := "bloem_contract_" + searchMirrorContractRandomHex(t, 6)
	master := newSearchMirrorContractClient(t, server.url, server.masterKey)

	var created struct {
		Key string `json:"key"`
		UID string `json:"uid"`
	}
	err := master.do(ctx, http.MethodPost, "/keys", map[string]any{
		"name":        "bloem-search-mirror-contract-" + prefix,
		"description": "Bloem search mirror contract test, deleted when the case ends",
		"actions":     searchMirrorKeyActions,
		"indexes":     []string{prefix + "*"},
		"expiresAt":   nil,
	}, &created)
	if err != nil {
		t.Fatalf("create the case's limited key: %v", err)
	}
	if created.Key == "" {
		t.Fatalf("create the case's limited key: the response carried no key")
	}
	if server.fake == nil {
		t.Cleanup(func() { cleanUpSearchMirrorContractCase(t, master, prefix, created.UID) })
	}
	return searchMirrorContractCase{
		server: server,
		prefix: prefix,
		key:    created.Key,
		client: newSearchMirrorContractClient(t, server.url, created.Key),
		master: master,
	}
}

// cleanUpSearchMirrorContractCase deletes the case's indexes and its key from the real server.
func cleanUpSearchMirrorContractCase(t *testing.T, master *meilisearchClient, prefix, keyUID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	uids, err := master.ListIndexUIDs(ctx)
	if err != nil {
		t.Errorf("clean up: list indexes: %v", err)
	}
	for _, uid := range uids {
		if !strings.HasPrefix(uid, prefix) {
			continue
		}
		ref, err := master.DeleteIndex(ctx, uid)
		if err == nil {
			err = master.WaitTask(ctx, ref)
		}
		if err != nil {
			t.Errorf("clean up: delete index %s: %v", uid, err)
		}
	}
	if keyUID != "" {
		if err := master.do(ctx, http.MethodDelete, "/keys/"+url.PathEscape(keyUID), nil, nil); err != nil {
			t.Errorf("clean up: delete the case's key: %v", err)
		}
	}
}

func newSearchMirrorContractClient(t *testing.T, rawURL, key string) *meilisearchClient {
	t.Helper()
	client, err := newMeilisearchClient(rawURL, key, searchMirrorContractTimeout)
	if err != nil {
		t.Fatalf("newMeilisearchClient: %v", err)
	}
	return client
}

func searchMirrorContractRandomHex(t *testing.T, size int) string {
	t.Helper()
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(buf)
}

func searchMirrorContractContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func searchMirrorContractDocs(prefix string, ids ...string) []catalogSearchDocument {
	docs := make([]catalogSearchDocument, 0, len(ids))
	for _, id := range ids {
		docs = append(docs, catalogSearchDocument{ContentID: id, Type: "movie", Title: prefix + " " + id, SchemaVersion: 1})
	}
	return docs
}

// mustWaitSearchMirrorTask fails the test unless the call that enqueued ref succeeded and the
// task then succeeded.
func mustWaitSearchMirrorTask(t *testing.T, client *meilisearchClient, what string, ref meilisearchTaskRef, err error) {
	t.Helper()
	if err == nil {
		err = client.WaitTask(searchMirrorContractContext(t), ref)
	}
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// makeSearchMirrorContractIndex creates uid holding docs, with settings when they are given.
func makeSearchMirrorContractIndex(t *testing.T, client *meilisearchClient, uid string, docs []catalogSearchDocument, settings map[string]any) {
	t.Helper()
	ctx := searchMirrorContractContext(t)
	ref, err := client.CreateIndex(ctx, uid)
	mustWaitSearchMirrorTask(t, client, "create "+uid, ref, err)
	if settings != nil {
		ref, err = client.UpdateSettings(ctx, uid, settings)
		mustWaitSearchMirrorTask(t, client, "settings of "+uid, ref, err)
	}
	ref, err = client.AddDocuments(ctx, uid, docs)
	mustWaitSearchMirrorTask(t, client, "add documents to "+uid, ref, err)
}

// getSearchMirrorContractDocument reads one document the way the marker read does.
func getSearchMirrorContractDocument(ctx context.Context, client *meilisearchClient, uid, id string) (catalogSearchDocument, error) {
	var doc catalogSearchDocument
	err := client.do(ctx, http.MethodGet, "/indexes/"+url.PathEscape(uid)+"/documents/"+url.PathEscape(id), nil, &doc)
	return doc, err
}

// assertSearchMirrorContractDocuments checks that uid holds exactly want: the count matches and
// every wanted document reads back with its title.
func assertSearchMirrorContractDocuments(t *testing.T, client *meilisearchClient, uid string, want []catalogSearchDocument) {
	t.Helper()
	ctx := searchMirrorContractContext(t)
	count, err := client.Stats(ctx, uid)
	if err != nil {
		t.Fatalf("stats of %s: %v", uid, err)
	}
	if count != len(want) {
		t.Fatalf("%s holds %d documents, want %d", uid, count, len(want))
	}
	for _, doc := range want {
		got, err := getSearchMirrorContractDocument(ctx, client, uid, doc.ContentID)
		if err != nil {
			t.Fatalf("read %s from %s: %v", doc.ContentID, uid, err)
		}
		if got.ContentID != doc.ContentID || got.Title != doc.Title {
			t.Fatalf("%s in %s = %+v, want title %q", doc.ContentID, uid, got, doc.Title)
		}
	}
}

func assertSearchMirrorContractDocumentAbsent(t *testing.T, client *meilisearchClient, uid, id string) {
	t.Helper()
	_, err := getSearchMirrorContractDocument(searchMirrorContractContext(t), client, uid, id)
	httpErr, ok := errors.AsType[*meilisearchHTTPError](err)
	if !ok || httpErr.StatusCode != http.StatusNotFound || httpErr.Code != "document_not_found" {
		t.Fatalf("read %s from %s: err = %v, want 404 document_not_found", id, uid, err)
	}
}

func assertSearchMirrorContractIndexExists(t *testing.T, client *meilisearchClient, uid string, want bool) {
	t.Helper()
	got, err := client.indexExists(searchMirrorContractContext(t), uid)
	if err != nil {
		t.Fatalf("indexExists(%s): %v", uid, err)
	}
	if got != want {
		t.Fatalf("indexExists(%s) = %v, want %v", uid, got, want)
	}
}

// assertSearchMirrorContractMaxTotalHits checks a setting moved with the index. Reading
// settings needs settings.get, which the mirror's key lacks, so it uses the master key.
func assertSearchMirrorContractMaxTotalHits(t *testing.T, c searchMirrorContractCase, uid string, want int) {
	t.Helper()
	settings, err := c.master.GetSettings(searchMirrorContractContext(t), uid)
	if err != nil {
		t.Fatalf("settings of %s: %v", uid, err)
	}
	if settings.Pagination.MaxTotalHits != want {
		t.Fatalf("%s pagination.maxTotalHits = %d, want %d", uid, settings.Pagination.MaxTotalHits, want)
	}
}

func searchMirrorContractMaxTotalHits(n int) map[string]any {
	return map[string]any{
		"filterableAttributes": []string{"type"},
		"pagination":           map[string]any{"maxTotalHits": n},
	}
}

func assertSearchMirrorErrorKind(t *testing.T, what string, err error, want searchMirrorErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want kind %q", what, want)
	}
	if got := classifySearchMirrorError(err); got != want {
		t.Fatalf("%s: kind %q for %v, want %q", what, got, err, want)
	}
}

// TestSearchMirrorContractPublishToNewName publishes a side index under a name no index has yet.
func TestSearchMirrorContractPublishToNewName(t *testing.T) {
	runSearchMirrorContract(t, func(t *testing.T, c searchMirrorContractCase) {
		side := c.prefix + "_mirrorcopy_1762850000"
		target := c.prefix + "_rebuild_1762840000"
		docs := searchMirrorContractDocs("new", "item-1", "item-2", "item-3")
		makeSearchMirrorContractIndex(t, c.client, side, docs, searchMirrorContractMaxTotalHits(4321))

		if err := c.client.publishIndex(searchMirrorContractContext(t), side, target); err != nil {
			t.Fatalf("publishIndex: %v", err)
		}

		assertSearchMirrorContractDocuments(t, c.client, target, docs)
		assertSearchMirrorContractMaxTotalHits(t, c, target, 4321)
		assertSearchMirrorContractIndexExists(t, c.client, side, false)
	})
}

// TestSearchMirrorContractPublishOverExisting publishes a side index over an index that already
// holds other documents and settings.
func TestSearchMirrorContractPublishOverExisting(t *testing.T) {
	runSearchMirrorContract(t, func(t *testing.T, c searchMirrorContractCase) {
		side := c.prefix + "_mirrorcopy_1762850000"
		target := c.prefix + "_rebuild_1762840000"
		old := searchMirrorContractDocs("old", "old-1", "old-2")
		makeSearchMirrorContractIndex(t, c.client, target, old, searchMirrorContractMaxTotalHits(1000))
		docs := searchMirrorContractDocs("new", "item-1", "item-2", "item-3")
		makeSearchMirrorContractIndex(t, c.client, side, docs, searchMirrorContractMaxTotalHits(4321))

		if err := c.client.publishIndex(searchMirrorContractContext(t), side, target); err != nil {
			t.Fatalf("publishIndex: %v", err)
		}

		assertSearchMirrorContractDocuments(t, c.client, target, docs)
		for _, doc := range old {
			assertSearchMirrorContractDocumentAbsent(t, c.client, target, doc.ContentID)
		}
		assertSearchMirrorContractMaxTotalHits(t, c, target, 4321)
		assertSearchMirrorContractIndexExists(t, c.client, side, false)
	})
}

// TestSearchMirrorContractMarkerRoundTrip reads and writes the identity marker.
func TestSearchMirrorContractMarkerRoundTrip(t *testing.T) {
	runSearchMirrorContract(t, func(t *testing.T, c searchMirrorContractCase) {
		ctx := searchMirrorContractContext(t)
		markerIndex := searchMirrorMarkerIndex(c.prefix)
		if markerIndex != c.prefix+"_bloem_target" {
			t.Fatalf("searchMirrorMarkerIndex(%q) = %q, want %q", c.prefix, markerIndex, c.prefix+"_bloem_target")
		}

		readMarker := func(want string) {
			t.Helper()
			got, err := c.client.readTargetMarker(ctx, c.prefix)
			if err != nil {
				t.Fatalf("readTargetMarker: %v", err)
			}
			if got != want {
				t.Fatalf("readTargetMarker = %q, want %q", got, want)
			}
		}

		readMarker("") // no marker index
		ref, err := c.client.CreateIndex(ctx, markerIndex)
		mustWaitSearchMirrorTask(t, c.client, "create the marker index", ref, err)
		readMarker("") // a marker index without the marker document

		if err := c.client.writeTargetMarker(ctx, c.prefix, "a"); err != nil {
			t.Fatalf("writeTargetMarker(a): %v", err)
		}
		readMarker("a")
		if err := c.client.writeTargetMarker(ctx, c.prefix, "b"); err != nil {
			t.Fatalf("writeTargetMarker(b): %v", err)
		}
		readMarker("b")

		count, err := c.client.Stats(ctx, markerIndex)
		if err != nil {
			t.Fatalf("stats of the marker index: %v", err)
		}
		if count != 1 {
			t.Fatalf("the marker index holds %d documents, want 1", count)
		}

		// A marker written where no marker index exists creates it.
		other := c.prefix + "_other"
		if err := c.client.writeTargetMarker(ctx, other, "c"); err != nil {
			t.Fatalf("writeTargetMarker on a new prefix: %v", err)
		}
		got, err := c.client.readTargetMarker(ctx, other)
		if err != nil || got != "c" {
			t.Fatalf("readTargetMarker on the new prefix = %q, %v; want c", got, err)
		}
	})
}

// TestSearchMirrorContractErrorKinds checks how each kind of trouble is classified.
func TestSearchMirrorContractErrorKinds(t *testing.T) {
	runSearchMirrorContract(t, func(t *testing.T, c searchMirrorContractCase) {
		ctx := searchMirrorContractContext(t)
		index := c.prefix + "_rebuild_1762840000"
		makeSearchMirrorContractIndex(t, c.client, index, searchMirrorContractDocs("kept", "kept-1"), nil)

		// Wrong key, no key, and a key that does not cover the index: key rejected.
		for _, key := range []string{"not-the-key", ""} {
			client := newSearchMirrorContractClient(t, c.server.url, key)
			_, err := client.indexExists(ctx, index)
			assertSearchMirrorErrorKind(t, "indexExists with a wrong or missing key", err, searchMirrorErrKeyRejected)
			_, err = client.readTargetMarker(ctx, c.prefix)
			assertSearchMirrorErrorKind(t, "readTargetMarker with a wrong or missing key", err, searchMirrorErrKeyRejected)
		}
		_, err := c.client.indexExists(ctx, "outside_"+c.prefix)
		assertSearchMirrorErrorKind(t, "indexExists outside the key's indexes", err, searchMirrorErrKeyRejected)

		// A task holding a document Meilisearch refuses fails as a whole: rejected.
		refused := []string{"bad id!"} // not a valid document id
		if c.server.fake != nil {
			c.server.fake.RefuseDocument("refused-1")
			refused = append(refused, "refused-1")
		}
		for _, id := range refused {
			docs := append(searchMirrorContractDocs("good", "good-1"), searchMirrorContractDocs("refused", id)...)
			ref, err := c.client.AddDocuments(ctx, index, docs)
			if err != nil {
				t.Fatalf("enqueue documents holding %q: %v", id, err)
			}
			err = c.client.WaitTask(ctx, ref)
			assertSearchMirrorErrorKind(t, fmt.Sprintf("a task holding %q", id), err, searchMirrorErrRejected)
			assertSearchMirrorContractDocuments(t, c.client, index, searchMirrorContractDocs("kept", "kept-1"))
		}

		// A request Meilisearch answers with another 4xx: rejected.
		err = c.client.publishIndex(ctx, c.prefix+"_mirrorcopy_absent", index)
		assertSearchMirrorErrorKind(t, "publishing a side index that does not exist", err, searchMirrorErrRejected)
		assertSearchMirrorContractDocuments(t, c.client, index, searchMirrorContractDocs("kept", "kept-1"))

		// A stopped server (the fake) or a closed port (beside the real one): unreachable.
		client := c.client
		if c.server.fake != nil {
			c.server.fake.Stop()
		} else {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			closedURL := "http://" + listener.Addr().String()
			_ = listener.Close()
			client = newSearchMirrorContractClient(t, closedURL, c.key)
		}
		_, err = client.indexExists(ctx, index)
		assertSearchMirrorErrorKind(t, "indexExists on a stopped server", err, searchMirrorErrUnreachable)
		_, err = client.readTargetMarker(ctx, c.prefix)
		assertSearchMirrorErrorKind(t, "readTargetMarker on a stopped server", err, searchMirrorErrUnreachable)
		err = client.writeTargetMarker(ctx, c.prefix, "a")
		assertSearchMirrorErrorKind(t, "writeTargetMarker on a stopped server", err, searchMirrorErrUnreachable)
		err = client.publishIndex(ctx, c.prefix+"_mirrorcopy_1762850000", index)
		assertSearchMirrorErrorKind(t, "publishIndex on a stopped server", err, searchMirrorErrUnreachable)
	})
}

// TestSearchMirrorContractKeyActionsAreEnough makes every call the mirror makes with a key
// holding only searchMirrorKeyActions. The fake does not check a key's actions, so this case
// runs on the real server only.
func TestSearchMirrorContractKeyActionsAreEnough(t *testing.T) {
	runSearchMirrorContract(t, func(t *testing.T, c searchMirrorContractCase) {
		if c.server.fake != nil {
			t.Skip("real only: the fake does not check a key's actions")
		}
		ctx := searchMirrorContractContext(t)
		step := func(what string, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("%s with the limited key: %v", what, err)
			}
		}
		target := c.prefix + "_rebuild_1762840000"
		side := c.prefix + "_mirrorcopy_1762850000"
		docs := searchMirrorContractDocs("new", "item-1", "item-2")

		step("health", c.client.Health(ctx))
		_, err := c.client.ListIndexUIDs(ctx)
		step("list indexes", err)
		ref, err := c.client.CreateIndex(ctx, side)
		mustWaitSearchMirrorTask(t, c.client, "create an index with the limited key", ref, err)
		ref, err = c.client.UpdateSettings(ctx, side, searchMirrorContractMaxTotalHits(4321))
		mustWaitSearchMirrorTask(t, c.client, "update settings with the limited key", ref, err)
		ref, err = c.client.AddDocuments(ctx, side, docs)
		mustWaitSearchMirrorTask(t, c.client, "add documents with the limited key", ref, err)
		_, err = c.client.Stats(ctx, side)
		step("stats", err)
		_, err = getSearchMirrorContractDocument(ctx, c.client, side, "item-1")
		step("read a document", err)
		ref, err = c.client.DeleteDocuments(ctx, side, []string{"item-2"})
		mustWaitSearchMirrorTask(t, c.client, "delete documents with the limited key", ref, err)
		_, err = c.client.indexExists(ctx, target)
		step("indexExists", err)
		step("writeTargetMarker", c.client.writeTargetMarker(ctx, c.prefix, "a"))
		_, err = c.client.readTargetMarker(ctx, c.prefix)
		step("readTargetMarker", err)
		step("publishIndex to a new name", c.client.publishIndex(ctx, side, target))
		makeSearchMirrorContractIndex(t, c.client, side, docs, searchMirrorContractMaxTotalHits(4321))
		step("publishIndex over an existing index", c.client.publishIndex(ctx, side, target))
		ref, err = c.client.DeleteIndex(ctx, target)
		mustWaitSearchMirrorTask(t, c.client, "delete an index with the limited key", ref, err)
	})
}

func TestClassifySearchMirrorError(t *testing.T) {
	timeout := &url.Error{Op: "Get", URL: "http://mirror", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}}
	cases := []struct {
		name string
		err  error
		want searchMirrorErrorKind
	}{
		{"nil", nil, searchMirrorErrNone},
		{"deadline", context.DeadlineExceeded, searchMirrorErrUnreachable},
		{"canceled", fmt.Errorf("apply page: %w", context.Canceled), searchMirrorErrUnreachable},
		{"network", timeout, searchMirrorErrUnreachable},
		{"decode", &meilisearchDecodeError{Err: errors.New("unexpected EOF")}, searchMirrorErrUnreachable},
		{"500", &meilisearchHTTPError{StatusCode: http.StatusInternalServerError}, searchMirrorErrUnreachable},
		{"503 wrapped", fmt.Errorf("publish: %w", &meilisearchHTTPError{StatusCode: http.StatusServiceUnavailable}), searchMirrorErrUnreachable},
		{"401", &meilisearchHTTPError{StatusCode: http.StatusUnauthorized, Code: "missing_authorization_header"}, searchMirrorErrKeyRejected},
		{"403", &meilisearchHTTPError{StatusCode: http.StatusForbidden, Code: "invalid_api_key"}, searchMirrorErrKeyRejected},
		{"400", &meilisearchHTTPError{StatusCode: http.StatusBadRequest, Code: "bad_request"}, searchMirrorErrRejected},
		{"404", &meilisearchHTTPError{StatusCode: http.StatusNotFound, Code: "index_not_found"}, searchMirrorErrRejected},
		{"413", &meilisearchHTTPError{StatusCode: http.StatusRequestEntityTooLarge, Code: "payload_too_large"}, searchMirrorErrRejected},
		{"failed task", errors.New("meilisearch task 7 failed: Document identifier is invalid"), searchMirrorErrRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySearchMirrorError(tc.err); got != tc.want {
				t.Fatalf("classifySearchMirrorError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestSearchMirrorFakeMeiliTestHelpers checks the fake's test controls that the contract cases
// do not reach.
func TestSearchMirrorFakeMeiliTestHelpers(t *testing.T) {
	ctx := searchMirrorContractContext(t)
	fake := newFakeMeili(t)
	client := fake.Client(t)

	fake.CreateIndexDirect("p_direct")
	if got := fake.Indexes(); len(got) != 1 || got[0] != "p_direct" {
		t.Fatalf("Indexes() = %v, want [p_direct]", got)
	}
	if fake.TaskCount() != 0 {
		t.Fatalf("CreateIndexDirect made %d tasks, want none", fake.TaskCount())
	}

	ref, err := client.UpdateSettings(ctx, "p_direct", map[string]any{"filterableAttributes": []string{"type"}})
	mustWaitSearchMirrorTask(t, client, "update settings", ref, err)
	if got := fake.Settings("p_direct")["filterableAttributes"]; fmt.Sprint(got) != "[type]" {
		t.Fatalf("Settings(p_direct)[filterableAttributes] = %v, want [type]", got)
	}
	docs := searchMirrorContractDocs("fake", "item-1", "item-2")
	ref, err = client.AddDocuments(ctx, "p_direct", docs)
	mustWaitSearchMirrorTask(t, client, "add documents", ref, err)
	if got := fake.Documents("p_direct"); len(got) != 2 || got["item-2"].Title != "fake item-2" {
		t.Fatalf("Documents(p_direct) = %+v, want item-1 and item-2", got)
	}
	if fake.TaskCount() != 2 {
		t.Fatalf("TaskCount() = %d, want 2", fake.TaskCount())
	}
	if fake.Documents("p_absent") != nil || fake.Settings("p_absent") != nil {
		t.Fatalf("Documents and Settings of an absent index should be nil")
	}

	// RefuseAll fails every task holding documents; nothing changes.
	fake.RefuseAll(true)
	ref, err = client.AddDocuments(ctx, "p_direct", searchMirrorContractDocs("fake", "item-3"))
	if err != nil {
		t.Fatalf("enqueue while refusing: %v", err)
	}
	assertSearchMirrorErrorKind(t, "add while refusing everything", client.WaitTask(ctx, ref), searchMirrorErrRejected)
	ref, err = client.DeleteDocuments(ctx, "p_direct", []string{"item-1"})
	if err != nil {
		t.Fatalf("enqueue a deletion while refusing: %v", err)
	}
	assertSearchMirrorErrorKind(t, "delete while refusing everything", client.WaitTask(ctx, ref), searchMirrorErrRejected)
	fake.RefuseAll(false)
	if got := fake.Documents("p_direct"); len(got) != 2 {
		t.Fatalf("refused tasks changed the documents: %+v", got)
	}

	// Hang holds every request until it is released or the caller gives up.
	fake.Hang(true)
	hungCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = client.indexExists(hungCtx, "p_direct")
	cancel()
	assertSearchMirrorErrorKind(t, "a request to a hung server", err, searchMirrorErrUnreachable)
	done := make(chan error, 1)
	go func() {
		_, err := client.indexExists(ctx, "p_direct")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a request returned while the server hangs: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	fake.Hang(false)
	if err := <-done; err != nil {
		t.Fatalf("a request released from the hang: %v", err)
	}

	// RequireKey turns authentication on; Client(t) sends the required key.
	fake.RequireKey("fake-key")
	if _, err := client.indexExists(ctx, "p_direct"); classifySearchMirrorError(err) != searchMirrorErrKeyRejected {
		t.Fatalf("a client without the key: err = %v, want key rejected", err)
	}
	if ok, err := fake.Client(t).indexExists(ctx, "p_direct"); err != nil || !ok {
		t.Fatalf("Client(t) after RequireKey: %v, %v", ok, err)
	}
	if err := client.Health(ctx); err != nil {
		t.Fatalf("health needs no key: %v", err)
	}

	// A hung request is released when the server stops.
	keyed := fake.Client(t)
	fake.Hang(true)
	go func() {
		_, err := keyed.indexExists(ctx, "p_direct")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond) //nolint:forbidigo // gives the request time to reach the hang; one that has not is refused by Stop, also unreachable
	fake.Stop()
	assertSearchMirrorErrorKind(t, "a hung request when the server stops", <-done, searchMirrorErrUnreachable)
	_, err = fake.Client(t).indexExists(ctx, "p_direct")
	assertSearchMirrorErrorKind(t, "a request after Stop", err, searchMirrorErrUnreachable)
}

// TestSearchMirrorPublishIndexKeepsSideWhenTheSwapFails covers a swap task that fails after it
// was accepted, for example because the target appeared or vanished between the existence check
// and the swap. publishIndex must report it and must not delete side, which still holds the copy.
func TestSearchMirrorPublishIndexKeepsSideWhenTheSwapFails(t *testing.T) {
	for _, targetExists := range []bool{false, true} {
		t.Run(fmt.Sprintf("target exists %v", targetExists), func(t *testing.T) {
			var requests []string
			client := newMeilisearchTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				notFound := func() {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"Index not found.","code":"index_not_found","type":"invalid_request"}`))
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /indexes/side":
					_, _ = w.Write([]byte(`{"uid":"side","createdAt":"2026-11-11T09:00:00.000000001Z"}`))
				case "GET /indexes/target/stats":
					if !targetExists {
						notFound()
						return
					}
					_, _ = w.Write([]byte(`{"numberOfDocuments":2}`))
				case "GET /indexes/target":
					if !targetExists {
						notFound()
						return
					}
					_, _ = w.Write([]byte(`{"uid":"target","createdAt":"2026-11-10T09:00:00.000000001Z"}`))
				case "POST /swap-indexes":
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":null,"status":"enqueued","type":"indexSwap"}`))
				case "POST /indexes/target/documents/delete-batch":
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"taskUid":2,"indexUid":"target","status":"enqueued","type":"documentDeletion"}`))
				case "GET /tasks/2":
					if !targetExists {
						_, _ = w.Write([]byte(`{"uid":2,"status":"failed","error":{"message":"Index not found.","code":"index_not_found"}}`))
						return
					}
					_, _ = w.Write([]byte(`{"uid":2,"status":"succeeded"}`))
				default:
					w.WriteHeader(http.StatusTeapot)
				}
			})

			err := client.publishIndex(context.Background(), "side", "target")
			if err == nil || !strings.Contains(err.Error(), "did not swap") {
				t.Fatalf("publishIndex = %v, want an error saying Meilisearch did not swap them", err)
			}
			assertSearchMirrorErrorKind(t, "a swap that did not happen", err, searchMirrorErrRejected)
			for _, request := range requests {
				if strings.HasPrefix(request, "DELETE ") {
					t.Fatalf("publishIndex sent %s after a failed swap; requests: %v", request, requests)
				}
			}
		})
	}
}
