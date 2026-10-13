package bookrequests

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// LookupListRequest reads only this actor's exact native edition and format.
// This recovers durable receipts even when display metadata changed meanwhile.
func (s *Service) LookupListRequest(ctx context.Context, v Viewer, in CreateInput, ebook bool) (Request, error) {
	in, err := normalizeInput(in)
	if err != nil {
		return Request{}, err
	}
	kind := "audiobook"
	if ebook {
		kind = "book"
		if in.ISBN == "" {
			return Request{}, ErrInvalid
		}
		if err = checkEbookAuthority(ctx, s.pool, v, false); err != nil {
			return Request{}, err
		}
	}
	if _, err = readPolicyFor(ctx, s.pool, v, kind); err != nil {
		return Request{}, err
	}
	r, err := scan(s.pool.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE organization_id=$1 AND requested_by_user_id=$2 AND requested_by_profile_id=$3 AND media_type=$4 AND identity_key=$5 ORDER BY created_at DESC,id DESC LIMIT 1`, v.OrganizationID, v.UserID, v.ProfileID, kind, in.identity))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	return r, err
}
