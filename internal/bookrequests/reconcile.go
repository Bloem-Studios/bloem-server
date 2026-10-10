package bookrequests

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ISBN-10 and ISBN-13 represent the same edition. Title/author similarities
// cannot establish library fulfillment; requests without identifiers wait.
func isbnVariants(isbn string) []string {
	out := []string{isbn}
	if len(isbn) != 13 || isbn[:3] != "978" {
		return out
	}
	ten := isbn[3:12]
	sum := 0
	for i, c := range ten {
		sum += int(c-'0') * (10 - i)
	}
	check := (11 - sum%11) % 11
	if check == 10 {
		ten += "X"
	} else {
		ten += fmt.Sprint(check)
	}
	return append(out, ten)
}

// findLibraryMatch is bounded by active ownership/entitlement and dedicated
// audiobook folders with actual media files. An ebook sharing its ISBN, or an
// audiobook only available to another organization, cannot fulfill this lane.
func findLibraryMatch(ctx context.Context, tx pgx.Tx, r Request) (string, error) {
	if r.MediaType != "audiobook" || r.ISBN == "" && r.ASIN == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRow(ctx, `
 SELECT i.content_id
 FROM media_items i
 JOIN media_files mf ON mf.content_id=i.content_id
 JOIN media_folders f ON f.id=mf.media_folder_id AND f.enabled AND lower(btrim(f.type)) IN ('audiobook','audiobooks')
 JOIN resource_owners ro ON ro.id=f.owner_id
 JOIN organizations o ON o.id=$1 AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 WHERE i.type='audiobook'
	AND ($2='' OR EXISTS(SELECT 1 FROM media_item_provider_ids ids WHERE ids.content_id=i.content_id AND ids.item_type='audiobook' AND lower(ids.provider)='isbn' AND upper(regexp_replace(ids.provider_id,'[-[:space:]]','','g'))=ANY($3) FOR SHARE))
	AND ($4='' OR EXISTS(SELECT 1 FROM media_item_provider_ids ids WHERE ids.content_id=i.content_id AND ids.item_type='audiobook' AND lower(ids.provider)='asin' AND upper(btrim(ids.provider_id))=$4 FOR SHARE))
 AND ((ro.kind='organization' AND ro.organization_id=$1)
   OR (ro.kind='platform' AND EXISTS(SELECT 1 FROM organization_entitlements e WHERE e.organization_id=$1 AND e.media_folder_id=f.id AND e.root_owner_id=ro.id AND e.status='active' FOR SHARE)))
 ORDER BY i.content_id,f.id LIMIT 1
 FOR SHARE OF i,mf,f,ro,o`, r.OrganizationID, r.ISBN, isbnVariants(r.ISBN), r.ASIN).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Service) Reconcile(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM bloem_book_requests WHERE media_type='audiobook' AND outcome='active' AND (status<>'completed' OR fulfilled_notified_at IS NULL) ORDER BY last_checked_at NULLS FIRST,created_at,id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, id := range ids {
		r, changed, err := s.reconcileOne(ctx, id)
		if err != nil {
			return completed, err
		}
		if changed {
			completed++
		}
		if r.ID == "" || r.Status != "completed" || s.notifier == nil {
			continue
		}
		if err = s.notifier.Fulfilled(ctx, r); err != nil {
			return completed, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE bloem_book_requests SET fulfilled_notified_at=now() WHERE id=$1 AND fulfilled_notified_at IS NULL`, id)
		if err != nil {
			return completed, err
		}
		if tag.RowsAffected() == 1 {
			if n, ok := s.notifier.(interface {
				AnnounceFulfilled(context.Context, Request)
			}); ok {
				n.AnnounceFulfilled(ctx, r)
			}
		}
	}
	return completed, nil
}
func (s *Service) reconcileOne(ctx context.Context, id string) (Request, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, false, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND media_type='audiobook' AND outcome='active' AND (status<>'completed' OR fulfilled_notified_at IS NULL) FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, ErrNotFound) {
		return Request{}, false, nil
	}
	if err != nil {
		return r, false, err
	}
	changed := false
	if r.Status != "completed" {
		match, err := findLibraryMatch(ctx, tx, r)
		if err != nil {
			return r, false, err
		}
		if match != "" {
			r, err = scan(tx.QueryRow(ctx, `UPDATE bloem_book_requests SET status='completed',fulfilment='library',library_content_id=$2,completed_at=now(),revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+selectColumns, id, match))
			if err != nil {
				return r, false, err
			}
			changed = true
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_book_requests SET last_checked_at=now() WHERE id=$1`, id); err != nil {
		return r, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return r, false, err
	}
	return r, changed, nil
}
