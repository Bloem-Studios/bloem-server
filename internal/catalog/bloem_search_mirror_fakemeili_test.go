package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMeili is an in-process Meilisearch for the search mirror's tests. It answers the calls the
// mirror makes the way getmeili/meilisearch:v1.56.0 does; the contract cases in
// bloem_search_mirror_meili_test.go run against both to keep it so.
//
// What it copies from 1.56:
//   - Writes are tasks: the request answers 202 with a taskUid, and GET /tasks/{uid} tells how
//     the task ended. A task that fails changes nothing.
//   - Creating an index that exists, deleting one that does not, deleting documents from an
//     index that does not exist and a swap that cannot be made are accepted (202) and then fail
//     as tasks, with Meilisearch's error codes.
//   - Adding documents or settings to an index that does not exist creates it.
//   - Swaps take "rename": with rename the second index must not exist and the first is renamed
//     to it; without it both must exist and trade contents. Documents, settings, primary key and
//     createdAt move with the contents.
//   - Document ids must be 1-511 bytes of a-z A-Z 0-9 - _ (invalid_document_id), and a document
//     without its primary key is refused (missing_document_id).
//   - Keys: without RequireKey every request is accepted. With it, every route but /health needs
//     a bearer key: no header is 401 missing_authorization_header, an unknown key or an index the
//     key does not cover is 403 invalid_api_key. Keys made with POST /keys are limited to their
//     index patterns ("*" or a prefix ending in "*"). A limited key cannot see a task that has no
//     index uid, such as a swap: GET /tasks/{uid} answers task_not_found, as 1.56 does.
//
// What it does not copy: a key's actions are not checked (the real-server contract case
// TestSearchMirrorContractKeyActionsAreEnough covers them), search is not served, settings are
// stored as given rather than filled with defaults, and tasks finish when they are enqueued.
type fakeMeili struct {
	t      *testing.T
	server *httptest.Server

	mu          sync.Mutex
	masterKey   string              // "" until RequireKey: then every request is accepted
	keys        map[string][]string // POST /keys keys and their index patterns
	indexes     map[string]*fakeMeiliIndex
	tasks       []*fakeMeiliTask // by uid
	refusedDocs map[string]bool  // content ids whose tasks fail
	refuseAll   bool             // every task holding documents fails
	hang        chan struct{}    // non-nil while hanging; closed to release
	stopped     bool
	stopping    chan struct{} // closed by Stop, to release hanging requests
	clock       time.Time     // createdAt/updatedAt of the next change
}

// fakeMeiliIndex is one index: its documents (as JSON objects, keyed by primary key), settings
// and creation time.
type fakeMeiliIndex struct {
	primaryKey string
	docs       map[string]map[string]any
	settings   map[string]any
	createdAt  time.Time
	updatedAt  time.Time
}

type fakeMeiliTask struct {
	uid        int
	indexUID   string // "" for a swap
	kind       string
	status     string
	errMessage string
	errCode    string
	errType    string
	details    map[string]any
	enqueuedAt time.Time
}

// fakeMeiliError is an error Meilisearch would report, for a response or a failed task.
type fakeMeiliError struct {
	status  int
	code    string
	message string
	typ     string
}

func (e *fakeMeiliError) Error() string { return e.message }

var fakeMeiliDocumentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,511}$`)
var fakeMeiliIndexUID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,400}$`)

// fakeMeiliMaxPayloadBytes is Meilisearch's default http-payload-size-limit, 100 MB.
const fakeMeiliMaxPayloadBytes = 100 * 1024 * 1024

// newFakeMeili starts a fake Meilisearch that accepts every request until RequireKey is called.
// It stops when the test ends.
func newFakeMeili(t *testing.T) *fakeMeili {
	t.Helper()
	f := &fakeMeili{
		t:           t,
		keys:        map[string][]string{},
		indexes:     map[string]*fakeMeiliIndex{},
		refusedDocs: map[string]bool{},
		stopping:    make(chan struct{}),
		clock:       time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Stop)
	return f
}

// URL is the fake's address.
func (f *fakeMeili) URL() string { return f.server.URL }

// Client returns a client for the fake that sends the key RequireKey set, if any.
func (f *fakeMeili) Client(t *testing.T) *meilisearchClient {
	t.Helper()
	f.mu.Lock()
	key := f.masterKey
	f.mu.Unlock()
	client, err := newMeilisearchClient(f.URL(), key, 5*time.Second)
	if err != nil {
		t.Fatalf("newMeilisearchClient: %v", err)
	}
	return client
}

// Stop shuts the fake down. Requests hanging at that moment fail as a dropped connection, and
// from then on nothing listens at URL, so every call is refused. Stop may be called again.
func (f *fakeMeili) Stop() {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	f.stopped = true
	close(f.stopping)
	f.mu.Unlock()
	f.server.CloseClientConnections()
	f.server.Close()
}

// RequireKey makes key the master key: from now on every request but /health needs it, or a key
// made with POST /keys.
func (f *fakeMeili) RequireKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.masterKey = key
}

// RefuseDocument makes every task that holds contentID (an addition or a deletion) fail, as a
// task holding a document Meilisearch refuses does. Nothing in such a task is applied.
func (f *fakeMeili) RefuseDocument(contentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusedDocs[contentID] = true
}

// RefuseAll makes every task that holds documents (additions and deletions) fail while on, as a
// server that cannot write (a full disk, a broken index) does. Index, settings and swap tasks
// still succeed.
func (f *fakeMeili) RefuseAll(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuseAll = on
}

// Hang makes every request, /health included, wait while on, until Hang(false), the caller gives
// up or Stop. Requests already waiting are released by Hang(false).
func (f *fakeMeili) Hang(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case on && f.hang == nil:
		f.hang = make(chan struct{})
	case !on && f.hang != nil:
		close(f.hang)
		f.hang = nil
	}
}

// Indexes returns every index uid, sorted.
func (f *fakeMeili) Indexes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.indexes))
}

// Documents returns uid's documents by content id, or nil when uid does not exist.
func (f *fakeMeili) Documents(uid string) map[string]catalogSearchDocument {
	f.mu.Lock()
	defer f.mu.Unlock()
	index, ok := f.indexes[uid]
	if !ok {
		return nil
	}
	out := make(map[string]catalogSearchDocument, len(index.docs))
	for id, raw := range index.docs {
		data, err := json.Marshal(raw)
		if err != nil {
			f.t.Fatalf("fake meilisearch: encode document %s in %s: %v", id, uid, err)
		}
		var doc catalogSearchDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			f.t.Fatalf("fake meilisearch: decode document %s in %s: %v", id, uid, err)
		}
		out[id] = doc
	}
	return out
}

// Settings returns a copy of the settings uid was given, or nil when uid does not exist.
func (f *fakeMeili) Settings(uid string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	index, ok := f.indexes[uid]
	if !ok {
		return nil
	}
	return fakeMeiliCopyJSON(f.t, index.settings)
}

// TaskCount is how many tasks the fake has enqueued, failed ones included.
func (f *fakeMeili) TaskCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
}

// CreateIndexDirect creates an empty index with primary key content_id, without a task. An
// index that exists is left as it is.
func (f *fakeMeili) CreateIndexDirect(uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.indexes[uid]; !ok {
		f.indexes[uid] = f.newIndexLocked("content_id")
	}
}

// --- HTTP -----------------------------------------------------------------------------------

func (f *fakeMeili) serve(w http.ResponseWriter, r *http.Request) {
	f.waitWhileHanging(r)

	body, err := io.ReadAll(io.LimitReader(r.Body, fakeMeiliMaxPayloadBytes+1))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if len(body) > fakeMeiliMaxPayloadBytes {
		fakeMeiliWriteError(w, &fakeMeiliError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large",
			message: "The provided payload reached the size limit. The maximum accepted payload size is 100 MiB."})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		panic(http.ErrAbortHandler)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path == "/health" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		fakeMeiliWriteJSON(w, http.StatusOK, map[string]string{"status": "available"})
		return
	}
	key, authErr := f.authenticateLocked(r)
	if authErr != nil {
		fakeMeiliWriteError(w, authErr)
		return
	}
	status, out, apiErr := f.routeLocked(r.Method, parts, r.URL.Query(), body, key)
	if apiErr != nil {
		fakeMeiliWriteError(w, apiErr)
		return
	}
	if status == http.StatusMethodNotAllowed {
		w.WriteHeader(status)
		return
	}
	fakeMeiliWriteJSON(w, status, out)
}

// waitWhileHanging holds the request while Hang is on. A request released by Stop is dropped
// without an answer.
func (f *fakeMeili) waitWhileHanging(r *http.Request) {
	for {
		f.mu.Lock()
		hang, stopped := f.hang, f.stopped
		f.mu.Unlock()
		if stopped {
			panic(http.ErrAbortHandler)
		}
		if hang == nil {
			return
		}
		select {
		case <-hang:
		case <-f.stopping:
		case <-r.Context().Done():
			panic(http.ErrAbortHandler)
		}
	}
}

// fakeMeiliKey is the key a request was accepted with.
type fakeMeiliKey struct {
	master  bool
	indexes []string // the patterns of a limited key
}

func (k fakeMeiliKey) covers(uid string) bool {
	if k.master {
		return true
	}
	for _, pattern := range k.indexes {
		if pattern == "*" || pattern == uid || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(uid, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
}

func (f *fakeMeili) authenticateLocked(r *http.Request) (fakeMeiliKey, *fakeMeiliError) {
	if f.masterKey == "" {
		return fakeMeiliKey{master: true}, nil
	}
	header := r.Header.Get("Authorization")
	if header == "" {
		return fakeMeiliKey{}, &fakeMeiliError{status: http.StatusUnauthorized, code: "missing_authorization_header", typ: "auth",
			message: "The Authorization header is missing. It must use the bearer authorization method."}
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	switch {
	case ok && token == f.masterKey:
		return fakeMeiliKey{master: true}, nil
	case ok && f.keys[token] != nil:
		return fakeMeiliKey{indexes: f.keys[token]}, nil
	}
	return fakeMeiliKey{}, fakeMeiliInvalidKey()
}

func fakeMeiliInvalidKey() *fakeMeiliError {
	return &fakeMeiliError{status: http.StatusForbidden, code: "invalid_api_key", typ: "auth", message: "The provided API key is invalid."}
}

func (f *fakeMeili) routeLocked(method string, parts []string, query map[string][]string, body []byte, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	notAllowed := func() (int, any, *fakeMeiliError) { return http.StatusMethodNotAllowed, nil, nil }
	indexRoute := func(uid string) *fakeMeiliError {
		if !key.covers(uid) {
			return &fakeMeiliError{status: http.StatusForbidden, code: "invalid_api_key", typ: "auth",
				message: fmt.Sprintf("The API key cannot acces the index `%s`, authorized indexes are %q.", uid, key.indexes)}
		}
		return nil
	}

	switch {
	case len(parts) == 1 && parts[0] == "keys":
		if method != http.MethodPost {
			return notAllowed()
		}
		return f.createKeyLocked(body, key)
	case len(parts) == 1 && parts[0] == "swap-indexes":
		if method != http.MethodPost {
			return notAllowed()
		}
		return f.swapLocked(body, key)
	case len(parts) == 2 && parts[0] == "tasks":
		if method != http.MethodGet {
			return notAllowed()
		}
		return f.getTaskLocked(parts[1], key)
	case len(parts) == 1 && parts[0] == "indexes":
		switch method {
		case http.MethodGet:
			return f.listIndexesLocked(query, key)
		case http.MethodPost:
			return f.createIndexLocked(body, key)
		}
		return notAllowed()
	case len(parts) >= 2 && parts[0] == "indexes":
		uid := parts[1]
		if err := indexRoute(uid); err != nil {
			return 0, nil, err
		}
		switch {
		case len(parts) == 2:
			switch method {
			case http.MethodGet:
				index, err := f.indexLocked(uid)
				if err != nil {
					return 0, nil, err
				}
				return http.StatusOK, fakeMeiliIndexView(uid, index), nil
			case http.MethodDelete:
				return f.enqueueLocked(uid, "indexDeletion", nil, f.deleteIndexTask(uid))
			}
			return notAllowed()
		case len(parts) == 3 && parts[2] == "stats":
			if method != http.MethodGet {
				return notAllowed()
			}
			index, err := f.indexLocked(uid)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusOK, map[string]any{"numberOfDocuments": len(index.docs), "isIndexing": false}, nil
		case len(parts) == 3 && parts[2] == "settings":
			switch method {
			case http.MethodGet:
				index, err := f.indexLocked(uid)
				if err != nil {
					return 0, nil, err
				}
				return http.StatusOK, index.settings, nil
			case http.MethodPatch:
				return f.updateSettingsLocked(uid, body)
			}
			return notAllowed()
		case len(parts) == 3 && parts[2] == "documents":
			if method != http.MethodPost {
				return notAllowed()
			}
			return f.addDocumentsLocked(uid, body)
		case len(parts) == 4 && parts[2] == "documents" && parts[3] == "delete-batch":
			if method != http.MethodPost {
				return notAllowed()
			}
			return f.deleteDocumentsLocked(uid, body)
		case len(parts) == 4 && parts[2] == "documents":
			if method != http.MethodGet {
				return notAllowed()
			}
			index, err := f.indexLocked(uid)
			if err != nil {
				return 0, nil, err
			}
			doc, ok := index.docs[parts[3]]
			if !ok {
				return 0, nil, &fakeMeiliError{status: http.StatusNotFound, code: "document_not_found",
					message: fmt.Sprintf("Document `%s` not found.", parts[3])}
			}
			return http.StatusOK, doc, nil
		}
	}
	return 0, nil, &fakeMeiliError{status: http.StatusNotFound, code: "not_found", message: "Not found."}
}

func (f *fakeMeili) indexLocked(uid string) (*fakeMeiliIndex, *fakeMeiliError) {
	index, ok := f.indexes[uid]
	if !ok {
		return nil, fakeMeiliIndexNotFound(uid)
	}
	return index, nil
}

func fakeMeiliIndexNotFound(uid string) *fakeMeiliError {
	return &fakeMeiliError{status: http.StatusNotFound, code: "index_not_found", message: fmt.Sprintf("Index `%s` not found.", uid)}
}

func fakeMeiliIndexView(uid string, index *fakeMeiliIndex) map[string]any {
	var primaryKey any
	if index.primaryKey != "" {
		primaryKey = index.primaryKey
	}
	return map[string]any{
		"uid":        uid,
		"primaryKey": primaryKey,
		"createdAt":  index.createdAt.Format(time.RFC3339Nano),
		"updatedAt":  index.updatedAt.Format(time.RFC3339Nano),
	}
}

func (f *fakeMeili) createKeyLocked(body []byte, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	if !key.master {
		return 0, nil, fakeMeiliInvalidKey()
	}
	var req struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Actions     []string `json:"actions"`
		Indexes     []string `json:"indexes"`
		ExpiresAt   *string  `json:"expiresAt"`
	}
	if err := fakeMeiliDecode(body, &req); err != nil {
		return 0, nil, err
	}
	if len(req.Actions) == 0 || len(req.Indexes) == 0 {
		return 0, nil, &fakeMeiliError{status: http.StatusBadRequest, code: "missing_parameter", message: "`actions` and `indexes` are required."}
	}
	uid := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.keys)+1)
	secret := fmt.Sprintf("fake-meili-key-%d", len(f.keys)+1)
	f.keys[secret] = slices.Clone(req.Indexes)
	return http.StatusCreated, map[string]any{
		"name": req.Name, "description": req.Description, "key": secret, "uid": uid,
		"actions": req.Actions, "indexes": req.Indexes, "expiresAt": req.ExpiresAt,
	}, nil
}

func (f *fakeMeili) listIndexesLocked(query map[string][]string, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	limit, offset := 20, 0
	if v := query["limit"]; len(v) > 0 {
		limit, _ = strconv.Atoi(v[0])
	}
	if v := query["offset"]; len(v) > 0 {
		offset, _ = strconv.Atoi(v[0])
	}
	var visible []string
	for _, uid := range slices.Sorted(maps.Keys(f.indexes)) {
		if key.covers(uid) {
			visible = append(visible, uid)
		}
	}
	results := []map[string]any{}
	for i := offset; i < len(visible) && i < offset+limit; i++ {
		results = append(results, fakeMeiliIndexView(visible[i], f.indexes[visible[i]]))
	}
	return http.StatusOK, map[string]any{"results": results, "offset": offset, "limit": limit, "total": len(visible)}, nil
}

func (f *fakeMeili) createIndexLocked(body []byte, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	var req struct {
		UID        string  `json:"uid"`
		PrimaryKey *string `json:"primaryKey"`
	}
	if err := fakeMeiliDecode(body, &req); err != nil {
		return 0, nil, err
	}
	if !fakeMeiliIndexUID.MatchString(req.UID) {
		return 0, nil, &fakeMeiliError{status: http.StatusBadRequest, code: "invalid_index_uid",
			message: fmt.Sprintf("`%s` is not a valid index uid.", req.UID)}
	}
	if !key.covers(req.UID) {
		return 0, nil, fakeMeiliInvalidKey()
	}
	primaryKey := ""
	if req.PrimaryKey != nil {
		primaryKey = *req.PrimaryKey
	}
	return f.enqueueLocked(req.UID, "indexCreation", map[string]any{"primaryKey": req.PrimaryKey}, func() *fakeMeiliError {
		if _, ok := f.indexes[req.UID]; ok {
			return &fakeMeiliError{code: "index_already_exists", message: fmt.Sprintf("Index `%s` already exists.", req.UID)}
		}
		f.indexes[req.UID] = f.newIndexLocked(primaryKey)
		return nil
	})
}

func (f *fakeMeili) deleteIndexTask(uid string) func() *fakeMeiliError {
	return func() *fakeMeiliError {
		if _, ok := f.indexes[uid]; !ok {
			return fakeMeiliIndexNotFound(uid)
		}
		delete(f.indexes, uid)
		return nil
	}
}

func (f *fakeMeili) updateSettingsLocked(uid string, body []byte) (int, any, *fakeMeiliError) {
	var settings map[string]any
	if err := fakeMeiliDecode(body, &settings); err != nil {
		return 0, nil, err
	}
	return f.enqueueLocked(uid, "settingsUpdate", settings, func() *fakeMeiliError {
		index := f.indexOrCreateLocked(uid)
		maps.Copy(index.settings, settings)
		index.updatedAt = f.tickLocked()
		return nil
	})
}

func (f *fakeMeili) addDocumentsLocked(uid string, body []byte) (int, any, *fakeMeiliError) {
	var docs []map[string]any
	if err := fakeMeiliDecode(body, &docs); err != nil {
		return 0, nil, err
	}
	details := map[string]any{"receivedDocuments": len(docs), "indexedDocuments": 0}
	return f.enqueueLocked(uid, "documentAdditionOrUpdate", details, func() *fakeMeiliError {
		primaryKey := "content_id"
		if index, ok := f.indexes[uid]; ok && index.primaryKey != "" {
			primaryKey = index.primaryKey
		}
		ids := make([]string, 0, len(docs))
		for _, doc := range docs {
			id, ok := fakeMeiliDocumentIDOf(doc[primaryKey])
			switch {
			case doc[primaryKey] == nil:
				data, _ := json.Marshal(doc)
				return &fakeMeiliError{code: "missing_document_id", message: fmt.Sprintf("Document doesn't have a `%s` attribute: `%s`.", primaryKey, data)}
			case !ok || !fakeMeiliDocumentID.MatchString(id):
				data, _ := json.Marshal(doc[primaryKey])
				return &fakeMeiliError{code: "invalid_document_id", message: fmt.Sprintf("Document identifier `%s` is invalid. A document identifier can be of type integer or string, only composed of alphanumeric characters (a-z A-Z 0-9), hyphens (-) and underscores (_), and can not be more than 511 bytes.", data)}
			}
			ids = append(ids, id)
		}
		if err := f.refusalLocked(ids); err != nil {
			return err
		}
		index := f.indexOrCreateLocked(uid)
		if index.primaryKey == "" {
			index.primaryKey = primaryKey
		}
		for i, doc := range docs {
			index.docs[ids[i]] = doc
		}
		index.updatedAt = f.tickLocked()
		details["indexedDocuments"] = len(docs)
		return nil
	})
}

func (f *fakeMeili) deleteDocumentsLocked(uid string, body []byte) (int, any, *fakeMeiliError) {
	var ids []any
	if err := fakeMeiliDecode(body, &ids); err != nil {
		return 0, nil, err
	}
	details := map[string]any{"providedIds": len(ids), "deletedDocuments": 0, "originalFilter": nil}
	return f.enqueueLocked(uid, "documentDeletion", details, func() *fakeMeiliError {
		index, ok := f.indexes[uid]
		if !ok {
			return fakeMeiliIndexNotFound(uid)
		}
		keys := make([]string, 0, len(ids))
		for _, raw := range ids {
			id, _ := fakeMeiliDocumentIDOf(raw)
			keys = append(keys, id)
		}
		if err := f.refusalLocked(keys); err != nil {
			return err
		}
		deleted := 0
		for _, id := range keys {
			if _, ok := index.docs[id]; ok {
				delete(index.docs, id)
				deleted++
			}
		}
		index.updatedAt = f.tickLocked()
		details["deletedDocuments"] = deleted
		return nil
	})
}

// refusalLocked is the error of a task holding ids, when RefuseAll or RefuseDocument refuses it.
func (f *fakeMeili) refusalLocked(ids []string) *fakeMeiliError {
	if f.refuseAll {
		return &fakeMeiliError{code: "internal", typ: "internal", message: "fake meilisearch refuses every document"}
	}
	for _, id := range ids {
		if f.refusedDocs[id] {
			return &fakeMeiliError{code: "invalid_document_fields", message: fmt.Sprintf("fake meilisearch refuses document `%s`", id)}
		}
	}
	return nil
}

func (f *fakeMeili) swapLocked(body []byte, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	var swaps []struct {
		Indexes []string `json:"indexes"`
		Rename  bool     `json:"rename"`
	}
	if err := fakeMeiliDecode(body, &swaps); err != nil {
		return 0, nil, err
	}
	seen := map[string]bool{}
	details := make([]map[string]any, 0, len(swaps))
	for _, swap := range swaps {
		if len(swap.Indexes) != 2 {
			data, _ := json.Marshal(swap.Indexes)
			return 0, nil, &fakeMeiliError{status: http.StatusBadRequest, code: "invalid_swap_indexes",
				message: fmt.Sprintf("Two indexes must be given for each swap. The list `%s` contains %d indexes.", data, len(swap.Indexes))}
		}
		for _, uid := range swap.Indexes {
			if seen[uid] {
				return 0, nil, &fakeMeiliError{status: http.StatusBadRequest, code: "invalid_swap_duplicate_index_found",
					message: fmt.Sprintf("Indexes must be declared only once during a swap. `%s` was specified several times.", uid)}
			}
			seen[uid] = true
			if !key.covers(uid) {
				return 0, nil, fakeMeiliInvalidKey()
			}
		}
		details = append(details, map[string]any{"indexes": swap.Indexes, "rename": swap.Rename})
	}
	return f.enqueueLocked("", "indexSwap", map[string]any{"swaps": details}, func() *fakeMeiliError {
		// Every swap is checked before any is made: the task applies all or nothing.
		for _, swap := range swaps {
			a, b := swap.Indexes[0], swap.Indexes[1]
			if _, ok := f.indexes[a]; !ok {
				return fakeMeiliIndexNotFound(a)
			}
			_, bExists := f.indexes[b]
			switch {
			case swap.Rename && bExists:
				return &fakeMeiliError{code: "index_already_exists",
					message: fmt.Sprintf("Cannot rename `%s` to `%s` as the index already exists. Hint: You can remove `%s` first and then do your remove.", a, b, b)}
			case !swap.Rename && !bExists:
				return fakeMeiliIndexNotFound(b)
			}
		}
		for _, swap := range swaps {
			a, b := swap.Indexes[0], swap.Indexes[1]
			if swap.Rename {
				f.indexes[b] = f.indexes[a]
				delete(f.indexes, a)
				continue
			}
			f.indexes[a], f.indexes[b] = f.indexes[b], f.indexes[a]
		}
		return nil
	})
}

func (f *fakeMeili) getTaskLocked(rawUID string, key fakeMeiliKey) (int, any, *fakeMeiliError) {
	uid, err := strconv.Atoi(rawUID)
	notFound := &fakeMeiliError{status: http.StatusNotFound, code: "task_not_found", message: fmt.Sprintf("Task `%s` not found.", rawUID)}
	if err != nil || uid < 0 || uid >= len(f.tasks) {
		return 0, nil, notFound
	}
	task := f.tasks[uid]
	// A limited key sees a task only through its index: a swap, which has none, stays hidden.
	if !key.master && !slices.Contains(key.indexes, "*") && (task.indexUID == "" || !key.covers(task.indexUID)) {
		return 0, nil, notFound
	}
	var indexUID, taskErr any
	if task.indexUID != "" {
		indexUID = task.indexUID
	}
	if task.errCode != "" {
		errType := task.errType
		if errType == "" {
			errType = "invalid_request"
		}
		taskErr = map[string]string{"message": task.errMessage, "code": task.errCode, "type": errType,
			"link": "https://docs.meilisearch.com/errors#" + task.errCode}
	}
	at := task.enqueuedAt.Format(time.RFC3339Nano)
	return http.StatusOK, map[string]any{
		"uid": task.uid, "batchUid": task.uid, "indexUid": indexUID, "status": task.status, "type": task.kind,
		"canceledBy": nil, "details": task.details, "error": taskErr, "duration": "PT0S",
		"enqueuedAt": at, "startedAt": at, "finishedAt": at,
	}, nil
}

// enqueueLocked records a task and runs it at once: it succeeds when apply returns nil and fails
// with apply's error otherwise. The answer is Meilisearch's 202 summary.
func (f *fakeMeili) enqueueLocked(indexUID, kind string, details map[string]any, apply func() *fakeMeiliError) (int, any, *fakeMeiliError) {
	task := &fakeMeiliTask{uid: len(f.tasks), indexUID: indexUID, kind: kind, status: "succeeded", details: details, enqueuedAt: f.tickLocked()}
	f.tasks = append(f.tasks, task)
	if err := apply(); err != nil {
		task.status, task.errCode, task.errMessage, task.errType = "failed", err.code, err.message, err.typ
	}
	var summaryIndex any
	if indexUID != "" {
		summaryIndex = indexUID
	}
	return http.StatusAccepted, map[string]any{
		"taskUid": task.uid, "indexUid": summaryIndex, "status": "enqueued", "type": kind,
		"enqueuedAt": task.enqueuedAt.Format(time.RFC3339Nano),
	}, nil
}

func (f *fakeMeili) newIndexLocked(primaryKey string) *fakeMeiliIndex {
	now := f.tickLocked()
	return &fakeMeiliIndex{primaryKey: primaryKey, docs: map[string]map[string]any{}, settings: map[string]any{}, createdAt: now, updatedAt: now}
}

// indexOrCreateLocked returns uid, creating it without a primary key as Meilisearch does when
// documents or settings arrive for an index that does not exist.
func (f *fakeMeili) indexOrCreateLocked(uid string) *fakeMeiliIndex {
	index, ok := f.indexes[uid]
	if !ok {
		index = f.newIndexLocked("")
		f.indexes[uid] = index
	}
	return index
}

// tickLocked returns a new, later time each call, so every index has its own createdAt.
func (f *fakeMeili) tickLocked() time.Time {
	f.clock = f.clock.Add(time.Microsecond)
	return f.clock
}

// fakeMeiliDocumentIDOf turns a JSON document id (a string or an integer) into its key.
func fakeMeiliDocumentIDOf(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
	}
	return "", false
}

// fakeMeiliDecode decodes a request body as Meilisearch does: unknown fields are refused.
func fakeMeiliDecode(body []byte, out any) *fakeMeiliError {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return &fakeMeiliError{status: http.StatusBadRequest, code: "bad_request", message: err.Error()}
	}
	return nil
}

func fakeMeiliCopyJSON(t *testing.T, in map[string]any) map[string]any {
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("fake meilisearch: copy settings: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("fake meilisearch: copy settings: %v", err)
	}
	return out
}

func fakeMeiliWriteError(w http.ResponseWriter, err *fakeMeiliError) {
	typ := err.typ
	if typ == "" {
		typ = "invalid_request"
	}
	fakeMeiliWriteJSON(w, err.status, map[string]string{
		"message": err.message, "code": err.code, "type": typ, "link": "https://docs.meilisearch.com/errors#" + err.code,
	})
}

func fakeMeiliWriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
