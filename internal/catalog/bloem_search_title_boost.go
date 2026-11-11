package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// Bloem: title matches stay first in hybrid search.
//
// Meilisearch's hybrid search merges keyword and meaning results by weighted
// score, and in practice behaves like a switch: below a semantic ratio of
// about 0.6 a strong keyword hit wins, from 0.6 up the results are the
// meaning-only results. Everyday searches ("funny movie about a dog") need the
// meaning side, but at 0.6 a title typed with a typo drops out of the top 10
// (93% -> 78% on the live catalog, 2026-11-11).
//
// So every hybrid search that has at least two terms goes out as a federated
// multi-search with one more query: a keyword query over the title fields
// alone, requiring every term, weighted above the hybrid query. A title that
// holds all the typed words ranks first; everything else keeps the hybrid
// order. Meilisearch returns each document once.
//
// If Meilisearch rejects the federated request, the original request runs
// unchanged, so the boost never makes a search worse than before.

const bloemTitleBoostWeight = 1.2

var bloemTitleBoostAttributes = []string{"title", "original_title", "sort_title", "title_variants"}

// bloemTitleBoostApplies reports whether a query gets the title query: only
// hybrid queries of two or more terms, the same queries that get the meaning
// side at all. That also leaves out the empty-query semantic probe.
func bloemTitleBoostApplies(query string, hybrid *meilisearchHybridRequest) bool {
	return hybrid != nil && len(catalogSearchQueryTerms(query)) >= meilisearchShortHybridMinTerms
}

func bloemTitleQuery(base meilisearchFederatedQuery) meilisearchFederatedQuery {
	query := meilisearchFederatedQuery{
		IndexUID:             base.IndexUID,
		Query:                base.Query,
		Filter:               base.Filter,
		AttributesToRetrieve: base.AttributesToRetrieve,
		AttributesToSearchOn: append([]string(nil), bloemTitleBoostAttributes...),
		MatchingStrategy:     "all",
	}
	query.FederationOptions.Weight = base.FederationOptions.Weight * bloemTitleBoostWeight
	return query
}

// withBloemTitleBoost puts a title query after every hybrid query of the
// request. It reports false when no query qualifies.
func withBloemTitleBoost(req meilisearchFederatedSearchRequest) (meilisearchFederatedSearchRequest, bool) {
	queries := make([]meilisearchFederatedQuery, 0, len(req.Queries)+1)
	added := false
	for _, query := range req.Queries {
		queries = append(queries, query)
		if bloemTitleBoostApplies(query.Query, query.Hybrid) {
			queries = append(queries, bloemTitleQuery(query))
			added = true
		}
	}
	if !added {
		return req, false
	}
	req.Queries = queries
	return req, true
}

// bloemTitleBoostSearch runs a single-index hybrid search as a federated
// search with the title query. handled is false when the request does not
// qualify or Meilisearch rejected the federated form; the caller then runs
// the request as it was.
func (c *meilisearchClient) bloemTitleBoostSearch(ctx context.Context, uid string, req meilisearchSearchRequest) (meilisearchSearchResponse, bool, error) {
	if !bloemTitleBoostApplies(req.Query, req.Hybrid) {
		return meilisearchSearchResponse{}, false, nil
	}
	main := meilisearchFederatedQuery{
		IndexUID:             uid,
		Query:                req.Query,
		Filter:               req.Filter,
		AttributesToRetrieve: req.AttributesToRetrieve,
		AttributesToSearchOn: req.AttributesToSearchOn,
		MatchingStrategy:     req.MatchingStrategy,
		Vector:               req.Vector,
		Hybrid:               req.Hybrid,
	}
	main.FederationOptions.Weight = 1
	return c.bloemRunTitleBoost(ctx, meilisearchFederatedSearchRequest{
		Federation: meilisearchFederationOptions{Offset: req.Offset, Limit: req.Limit},
		Queries:    []meilisearchFederatedQuery{main, bloemTitleQuery(main)},
	})
}

// bloemTitleBoostFederatedSearch adds the title query to a federated search
// whose hybrid query qualifies, such as the media half of a search that mixes
// episodes and media.
func (c *meilisearchClient) bloemTitleBoostFederatedSearch(ctx context.Context, req meilisearchFederatedSearchRequest) (meilisearchSearchResponse, bool, error) {
	boosted, ok := withBloemTitleBoost(req)
	if !ok {
		return meilisearchSearchResponse{}, false, nil
	}
	return c.bloemRunTitleBoost(ctx, boosted)
}

func (c *meilisearchClient) bloemRunTitleBoost(ctx context.Context, req meilisearchFederatedSearchRequest) (meilisearchSearchResponse, bool, error) {
	var out meilisearchSearchResponse
	err := c.do(ctx, http.MethodPost, "/multi-search", req, &out)
	if err == nil {
		return out, true, nil
	}
	// Only a request Meilisearch answered and refused (a client error) is
	// retried in its original form. A timeout or an unreachable server would
	// fail the same way again, so it is returned as it is.
	var httpErr *meilisearchHTTPError
	if ctx.Err() != nil || !errors.As(err, &httpErr) || httpErr.StatusCode < 400 || httpErr.StatusCode >= 500 {
		return meilisearchSearchResponse{}, true, err
	}
	slog.WarnContext(ctx, "catalog search: title boost rejected; searching without it",
		"component", "catalog", "status", httpErr.StatusCode, "code", httpErr.Code)
	return meilisearchSearchResponse{}, false, nil
}
