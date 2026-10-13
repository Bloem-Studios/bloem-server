package api

import (
	"context"
	"errors"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type listCollectionRequests struct {
	domain *listCollectionDomain
	movies *requests.Service
	books  *bookrequests.Service
}

func (d *listCollectionRequests) requestContext(ctx context.Context, s listcollections.Subject) (context.Context, bookrequests.Viewer, error) {
	p := managedtracking.Profile{AccountID: strconv.Itoa(s.UserID), ProfileID: s.ProfileID, AccountActive: true, Active: true}
	if d == nil || d.domain == nil || d.domain.source == nil {
		return ctx, bookrequests.Viewer{}, listcollections.ErrAuthority
	}
	source := d.domain.source
	var err error
	if source.prepare != nil {
		ctx, err = source.prepare(ctx, p)
		if err != nil {
			return ctx, bookrequests.Viewer{}, err
		}
	}
	scope, err := source.scope(ctx, p)
	if err != nil {
		return ctx, bookrequests.Viewer{}, err
	}
	ctx = apimw.SetProfileID(access.SetScope(ctx, scope), s.ProfileID)
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return ctx, bookrequests.Viewer{}, listcollections.ErrAuthority
	}
	return ctx, bookrequests.Viewer{UserID: s.UserID, ProfileID: s.ProfileID, OrganizationID: tenant.OrganizationID, Admin: s.IsAdmin}, nil
}
func listRequestError(err error) (listcollections.RequestOutcome, error) {
	switch {
	case errors.Is(err, requests.ErrNotFound), errors.Is(err, bookrequests.ErrNotFound):
		return listcollections.RequestOutcome{}, nil
	case errors.Is(err, requests.ErrQuotaExceeded), errors.Is(err, bookrequests.ErrQuota):
		return listcollections.RequestOutcome{Status: "blocked", Reason: "quota"}, nil
	case errors.Is(err, requests.ErrForbidden), errors.Is(err, bookrequests.ErrForbidden), errors.Is(err, listcollections.ErrAuthority), errors.Is(err, managedtracking.ErrAuthority), errors.Is(err, requests.ErrUserBlocked):
		return listcollections.RequestOutcome{Status: "blocked", Reason: "denied"}, nil
	case errors.Is(err, bookrequests.ErrDisabled), errors.Is(err, requests.ErrRequestsDisabled):
		return listcollections.RequestOutcome{Status: "blocked", Reason: "disabled"}, nil
	case errors.Is(err, requests.ErrInvalidInput), errors.Is(err, bookrequests.ErrInvalid), errors.Is(err, listcollections.ErrRequestUnsupported):
		return listcollections.RequestOutcome{Status: "blocked", Reason: "unsupported"}, nil
	case errors.Is(err, requests.ErrAlreadyAvailable):
		return listcollections.RequestOutcome{Status: "cancelled", Reason: "available"}, nil
	case errors.Is(err, requests.ErrAlreadyRequested), errors.Is(err, bookrequests.ErrDuplicate):
		return listcollections.RequestOutcome{Status: "blocked", Reason: "already_requested"}, nil
	default:
		return listcollections.RequestOutcome{}, err
	}
}
func listMovieInput(e listcollections.Entry) requests.CreateRequestInput {
	id, _ := strconv.Atoi(e.ExternalIDs["tmdb"])
	kind := requests.MediaTypeMovie
	if e.Kind == "series" {
		kind = requests.MediaTypeSeries
	}
	return requests.CreateRequestInput{MediaType: kind, TMDBID: id, Title: e.Title}
}
func (d *listCollectionRequests) Lookup(ctx context.Context, s listcollections.Subject, e listcollections.Entry) (listcollections.RequestOutcome, error) {
	key, err := listcollections.RequestReplayKey(s, e)
	if err != nil {
		return listRequestError(err)
	}
	ctx, v, err := d.requestContext(ctx, s)
	if err != nil {
		return listRequestError(err)
	}
	if e.Kind == "movie" || e.Kind == "series" {
		if d.movies == nil {
			return listcollections.RequestOutcome{Status: "blocked", Reason: "configuration"}, nil
		}
		viewer := requests.Viewer{UserID: s.UserID, ProfileID: s.ProfileID, IsAdmin: s.IsAdmin}
		input := listMovieInput(e)
		r, err := d.movies.LookupListRequest(ctx, viewer, input, key)
		if errors.Is(err, requests.ErrNotFound) {
			r, err = d.movies.LookupExistingListRequest(ctx, viewer, input)
		}
		if err != nil {
			return listRequestError(err)
		}
		return listcollections.RequestOutcome{ID: r.ID, Status: "delivered"}, nil
	}
	if d.books == nil {
		return listcollections.RequestOutcome{Status: "blocked", Reason: "configuration"}, nil
	}
	r, err := d.books.LookupListRequest(ctx, v, bookrequests.CreateInput{Title: e.Title, ISBN: e.ExternalIDs["isbn"], ASIN: e.ExternalIDs["asin"]}, e.Kind == "ebook")
	if err != nil {
		return listRequestError(err)
	}
	return listcollections.RequestOutcome{ID: r.ID, Status: "delivered"}, nil
}
func (d *listCollectionRequests) Submit(ctx context.Context, s listcollections.Subject, e listcollections.Entry, key string) (listcollections.RequestOutcome, error) {
	ctx, v, err := d.requestContext(ctx, s)
	if err != nil {
		return listRequestError(err)
	}
	if e.Kind == "movie" || e.Kind == "series" {
		r, err := d.movies.CreateListRequest(ctx, requests.Viewer{UserID: s.UserID, ProfileID: s.ProfileID, IsAdmin: s.IsAdmin}, listMovieInput(e), key)
		if err != nil {
			return listRequestError(err)
		}
		return listcollections.RequestOutcome{ID: r.ID, Status: "delivered"}, nil
	}
	var r bookrequests.Request
	if e.Kind == "ebook" {
		r, _, err = d.books.CreateEbook(ctx, v, bookrequests.EbookCreateInput{Title: e.Title, ISBN: e.ExternalIDs["isbn"]}, key)
	} else {
		r, _, err = d.books.Create(ctx, v, bookrequests.CreateInput{Title: e.Title, ISBN: e.ExternalIDs["isbn"], ASIN: e.ExternalIDs["asin"]}, key)
	}
	if err != nil {
		// Recovery is scoped to native identity; it also handles a concurrent filing
		// from another bound list without changing attribution or spending again.
		if prior, lookupErr := d.Lookup(ctx, s, e); lookupErr == nil && prior.ID != "" {
			return prior, nil
		}
		return listRequestError(err)
	}
	return listcollections.RequestOutcome{ID: r.ID, Status: "delivered"}, nil
}
