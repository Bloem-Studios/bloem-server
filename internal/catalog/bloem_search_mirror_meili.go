package catalog

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The Meilisearch calls the search mirror needs beyond Silo's client: whether an index
// exists, the identity marker, and publishing a finished copy under its real name. They are
// methods on Silo's *meilisearchClient so they share its transport and error types, and live
// in this Bloem file so Silo's client file stays untouched.
//
// They are pinned to Meilisearch 1.56 by the contract cases in
// bloem_search_mirror_meili_test.go, which run against the in-process fake always and against
// getmeili/meilisearch:v1.56.0 when BLOEM_TEST_MEILISEARCH_URL is set.

// searchMirrorErrorKind is the kind of trouble a mirror call ran into. The kind decides what the
// mirror does next: unreachable spends no tries and moves no bookmark, key_rejected makes the
// mirror misconfigured, rejected spends a try and leads to a split and dead-letters.
type searchMirrorErrorKind string

const (
	searchMirrorErrNone        searchMirrorErrorKind = ""
	searchMirrorErrUnreachable searchMirrorErrorKind = "unreachable"  // net.Error, DeadlineExceeded, HTTP >= 500, decode errors
	searchMirrorErrKeyRejected searchMirrorErrorKind = "key_rejected" // HTTP 401/403 (metric kind "rejected")
	searchMirrorErrRejected    searchMirrorErrorKind = "rejected"     // a failed/canceled task or another 4xx
	searchMirrorErrDatabase    searchMirrorErrorKind = "database"     // Postgres errors (classified by callers)
)

// classifySearchMirrorError sorts a Meilisearch error the way
// (*MeilisearchSearchProvider).shouldTripCircuit does, with 401 and 403 split out:
//   - what trips Silo's circuit (a deadline, a network error, HTTP 5xx, an undecodable answer)
//     is unreachable;
//   - HTTP 401 and 403 are key_rejected. Meilisearch answers 403 too when the key's indexes do
//     not cover the index asked for;
//   - every other error is rejected: another 4xx, and a task that failed or was canceled, which
//     WaitTask reports as a plain error.
//
// A canceled context is the one departure. shouldTripCircuit leaves it alone because it says
// nothing about the server; the mirror files it under unreachable, the kind that spends no try
// and moves no bookmark, so a shutdown in the middle of a page never counts against the page.
func classifySearchMirrorError(err error) searchMirrorErrorKind {
	if err == nil {
		return searchMirrorErrNone
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return searchMirrorErrUnreachable
	}
	if httpErr, ok := errors.AsType[*meilisearchHTTPError](err); ok {
		switch {
		case httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden:
			return searchMirrorErrKeyRejected
		case httpErr.StatusCode >= http.StatusInternalServerError:
			return searchMirrorErrUnreachable
		default:
			return searchMirrorErrRejected
		}
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return searchMirrorErrUnreachable
	}
	if _, ok := errors.AsType[*meilisearchDecodeError](err); ok {
		return searchMirrorErrUnreachable
	}
	return searchMirrorErrRejected
}

const (
	// searchMirrorMarkerSuffix names the identity marker's index after the index prefix. The
	// main rebuild's cleanup deletes only "<prefix>_rebuild_*" names and the previous index, so
	// it never matches the marker.
	searchMirrorMarkerSuffix = "_bloem_target"
	// searchMirrorMarkerDocumentID is the one document in the marker index.
	searchMirrorMarkerDocumentID = "bloem_target"
	// searchMirrorPublishFenceID is a document id no catalog item has. publishIndex deletes it
	// from the target to learn when its swap has been processed.
	searchMirrorPublishFenceID = "bloem_publish_fence"
)

// searchMirrorMarkerIndex is the identity marker's index for an index prefix.
func searchMirrorMarkerIndex(prefix string) string {
	return prefix + searchMirrorMarkerSuffix
}

// searchMirrorMarkerDocument is the identity marker: content_id is the marker index's primary
// key, target_id the server's identity.
type searchMirrorMarkerDocument struct {
	ContentID string `json:"content_id"`
	TargetID  string `json:"target_id"`
}

// indexExists reports whether uid exists. It asks for the index's stats, which the mirror's key
// may read, and treats index_not_found as "no".
func (c *meilisearchClient) indexExists(ctx context.Context, uid string) (bool, error) {
	if _, err := c.Stats(ctx, uid); err != nil {
		if isMeilisearchIndexNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// readTargetMarker returns the identity the server carries for prefix, or "" when the marker
// index or its document is absent. Only Meilisearch's own index_not_found and
// document_not_found codes count as absent: a bare 404 from something in front of Meilisearch
// is an error, because reading "no identity" would make the mirror write a new one.
func (c *meilisearchClient) readTargetMarker(ctx context.Context, prefix string) (string, error) {
	var doc searchMirrorMarkerDocument
	endpoint := "/indexes/" + url.PathEscape(searchMirrorMarkerIndex(prefix)) + "/documents/" + url.PathEscape(searchMirrorMarkerDocumentID)
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &doc); err != nil {
		if httpErr, ok := errors.AsType[*meilisearchHTTPError](err); ok && httpErr.StatusCode == http.StatusNotFound {
			switch strings.TrimSpace(httpErr.Code) {
			case "index_not_found", "document_not_found":
				return "", nil
			}
		}
		return "", err
	}
	return doc.TargetID, nil
}

// writeTargetMarker sets the identity the server carries for prefix to id and waits until
// Meilisearch has stored it.
//
// It creates the marker index with content_id as its primary key, then replaces the marker
// document. When the index exists already, Meilisearch 1.56 accepts the creation and fails its
// task with index_already_exists; that task is not waited for. Tasks on one index run in the
// order they were enqueued, so the document is stored after the creation either way.
func (c *meilisearchClient) writeTargetMarker(ctx context.Context, prefix, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("search mirror marker id is empty")
	}
	index := searchMirrorMarkerIndex(prefix)
	if _, err := c.CreateIndex(ctx, index); err != nil {
		return fmt.Errorf("create the marker index %s: %w", index, err)
	}
	var task meilisearchTask
	docs := []searchMirrorMarkerDocument{{ContentID: searchMirrorMarkerDocumentID, TargetID: id}}
	if err := c.do(ctx, http.MethodPost, "/indexes/"+url.PathEscape(index)+"/documents", docs, &task); err != nil {
		return fmt.Errorf("write the marker in %s: %w", index, err)
	}
	if err := c.WaitTask(ctx, newMeilisearchTaskRef(task.TaskUID)); err != nil {
		return fmt.Errorf("write the marker in %s: %w", index, err)
	}
	return nil
}

// searchMirrorIndexSwap is one entry of POST /swap-indexes.
type searchMirrorIndexSwap struct {
	Indexes [2]string `json:"indexes"`
	Rename  bool      `json:"rename"`
}

// searchMirrorIndexInfo is what GET /indexes/{uid} says about an index. createdAt belongs to
// the index's contents: a swap or a rename moves it with them.
type searchMirrorIndexInfo struct {
	UID       string `json:"uid"`
	CreatedAt string `json:"createdAt"`
}

func (c *meilisearchClient) indexInfo(ctx context.Context, uid string) (searchMirrorIndexInfo, error) {
	var info searchMirrorIndexInfo
	err := c.do(ctx, http.MethodGet, "/indexes/"+url.PathEscape(uid), nil, &info)
	return info, err
}

// publishIndex puts side's contents (documents and settings) under target in one step and
// deletes side. Nothing is visible under target before the step.
//
// Meilisearch 1.56 accepts "rename": true in POST /swap-indexes (checked against
// getmeili/meilisearch:v1.56.0 on 2026-11-11 by the contract cases). So when target does not
// exist, side is renamed to it; when target exists, the two are swapped with "rename": false and
// side, now holding target's old contents, is deleted. 1.56 refuses a rename onto an existing
// index (index_already_exists) and a swap with a missing index (index_not_found), which is why
// the two cases differ. The create-an-empty-target fallback the plan kept for a server that
// refused rename is not needed.
//
// The swap task cannot be waited for with the mirror's key. A key limited to "<prefix>*" cannot
// read a swap task, because the task has no index uid: 1.56 answers GET /tasks/{uid} with
// task_not_found. So publishIndex never reads it:
//  1. It enqueues a deletion of a document no item has (searchMirrorPublishFenceID) from target
//     and waits for that. Meilisearch handles a swap before any index task enqueued after it, so
//     once the deletion is done the swap has been processed. The deletion fails with
//     index_not_found when the rename did not happen; that failure is expected and ignored.
//  2. It checks the swap took effect: target's createdAt must be the one side had before.
//
// Callers wait for every task on side before publishing it.
func (c *meilisearchClient) publishIndex(ctx context.Context, side, target string) error {
	sideInfo, err := c.indexInfo(ctx, side)
	if err != nil {
		return fmt.Errorf("publish %s: read it: %w", side, err)
	}
	targetExists, err := c.indexExists(ctx, target)
	if err != nil {
		return fmt.Errorf("publish %s as %s: %w", side, target, err)
	}
	swaps := []searchMirrorIndexSwap{{Indexes: [2]string{side, target}, Rename: !targetExists}}
	if err := c.do(ctx, http.MethodPost, "/swap-indexes", swaps, nil); err != nil {
		return fmt.Errorf("publish %s as %s: swap: %w", side, target, err)
	}

	var fence meilisearchTask
	fenceEndpoint := "/indexes/" + url.PathEscape(target) + "/documents/delete-batch"
	if err := c.do(ctx, http.MethodPost, fenceEndpoint, []string{searchMirrorPublishFenceID}, &fence); err != nil {
		return fmt.Errorf("publish %s as %s: wait for the swap: %w", side, target, err)
	}
	if err := c.WaitTask(ctx, newMeilisearchTaskRef(fence.TaskUID)); err != nil && classifySearchMirrorError(err) != searchMirrorErrRejected {
		return fmt.Errorf("publish %s as %s: wait for the swap: %w", side, target, err)
	}
	published, err := c.indexInfo(ctx, target)
	switch {
	case err != nil && !isMeilisearchIndexNotFound(err):
		return fmt.Errorf("publish %s as %s: check the swap: %w", side, target, err)
	case err != nil || published.CreatedAt != sideInfo.CreatedAt:
		return fmt.Errorf("publish %s as %s: Meilisearch did not swap them", side, target)
	}

	if !targetExists {
		return nil
	}
	ref, err := c.DeleteIndex(ctx, side)
	if err == nil {
		err = c.WaitTask(ctx, ref)
	}
	if err != nil {
		return fmt.Errorf("publish %s as %s: delete the old contents under %s: %w", side, target, side, err)
	}
	return nil
}
