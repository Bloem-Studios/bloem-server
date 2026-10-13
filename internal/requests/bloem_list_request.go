package requests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type listRequestWitnessKey struct{}
type listRequestWitness struct {
	id     string
	viewer Viewer
	kind   MediaType
	tmdb   int
}

func listRequestID(key string) (string, error) {
	if len(key) < 16 || len(key) > 128 || strings.ContainsAny(key, "\r\n\t ") {
		return "", ErrInvalidInput
	}
	hash := sha256.Sum256([]byte(key))
	// Request IDs are opaque strings. This owned namespace is disjoint from
	// ordinary native generated IDs, and survives a lost commit response.
	return "blist_" + hex.EncodeToString(hash[:]), nil
}

// LookupListRequest uses the native authorized read, including after an issued
// request ceased to be active. Replays cannot silently file a replacement.
func (s *Service) LookupListRequest(ctx context.Context, viewer Viewer, in CreateRequestInput, key string) (*Request, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("request service is not configured")
	}
	if err := validateViewer(viewer); err != nil {
		return nil, err
	}
	in, err := normalizeCreateInput(in)
	if err != nil {
		return nil, err
	}
	id, err := listRequestID(key)
	if err != nil {
		return nil, err
	}
	r, err := s.GetRequest(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	if r.RequestedByUserID != viewer.UserID || r.RequestedByProfileID != viewer.ProfileID {
		return nil, ErrForbidden
	}
	if r.MediaType != in.MediaType || r.TMDBID != in.TMDBID {
		return nil, ErrInvalidInput
	}
	return r, nil
}

// CreateListRequest retains the native policy, quota, metadata, routing and
// repository. The sealed context witness changes only the ID at native create;
// it does not wrap the store or erase optional tenant/policy capabilities.
func (s *Service) CreateListRequest(ctx context.Context, viewer Viewer, in CreateRequestInput, key string) (*Request, error) {
	if prior, err := s.LookupListRequest(ctx, viewer, in, key); !errors.Is(err, ErrNotFound) {
		return prior, err
	}
	id, err := listRequestID(key)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeCreateInput(in)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, listRequestWitnessKey{}, listRequestWitness{id: id, viewer: viewer, kind: normalized.MediaType, tmdb: normalized.TMDBID})
	r, err := s.CreateRequest(ctx, viewer, in)
	if err == nil {
		return r, nil
	}
	// The native transaction can commit before the caller loses its receipt,
	// or another replica can win the same key while this one waits on quota.
	if prior, lookupErr := s.LookupListRequest(ctx, viewer, in, key); lookupErr == nil {
		return prior, nil
	}
	return nil, err
}

func bloemListRequestID(ctx context.Context, ordinary string, viewer Viewer, in CreateRequestInput) string {
	if witness, ok := ctx.Value(listRequestWitnessKey{}).(listRequestWitness); ok && witness.viewer == viewer && witness.kind == in.MediaType && witness.tmdb == in.TMDBID {
		return witness.id
	}
	return ordinary
}

// LookupExistingListRequest uses a scoped native identity index rather than a
// bounded display page. It never returns another household profile's request.
func (s *Service) LookupExistingListRequest(ctx context.Context, v Viewer, in CreateRequestInput) (*Request, error) {
	normalized, err := normalizeCreateInput(in)
	if err != nil {
		return nil, err
	}
	if err = validateViewer(v); err != nil {
		return nil, err
	}
	lookup, ok := s.store.(interface {
		FindListRequest(context.Context, Viewer, CreateRequestInput) (string, error)
	})
	if !ok {
		return nil, ErrNotFound
	}
	id, err := lookup.FindListRequest(ctx, v, normalized)
	if err != nil {
		return nil, err
	}
	r, err := s.GetRequest(ctx, v, id)
	if err != nil {
		return nil, err
	}
	if r.RequestedByUserID != v.UserID || r.RequestedByProfileID != v.ProfileID || r.MediaType != normalized.MediaType || r.TMDBID != normalized.TMDBID {
		return nil, ErrForbidden
	}
	return r, nil
}
func (r *Repository) FindListRequest(ctx context.Context, v Viewer, in CreateRequestInput) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id FROM media_requests WHERE requested_by_user_id=$1 AND requested_by_profile_id=$2 AND media_type=$3 AND tmdb_id=$4 ORDER BY created_at DESC,id DESC LIMIT 1`, v.UserID, v.ProfileID, in.MediaType, in.TMDBID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
