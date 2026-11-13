package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
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
// Since Silo 784c406b6 (#4058) Silo runs every hybrid search as federated
// queries of its own: a keyword query weighted 1-r, a meaning-only query (an
// empty q at semantic ratio 1) weighted r, and a keyword episode query weighted
// 1-r when the search mixes episodes with other media. Each keyword query of
// two or more terms gets the title queries, weighted above the heaviest query
// in the request, and Silo's weights stay as they are.
//
// Before that, a search that mixed episodes and other types was a federated
// search of one hybrid query and a keyword-only episode query at weight 1,
// which balanced only at Silo's default ratio of 0.5. At 0.6 episodes whose
// plots share a word filled everyday searches (40 of the top 10s of 22 such
// searches, against 18 at 0.5). Keyword-only queries next to a hybrid query
// that mixes keyword and meaning with ratio r are therefore weighted (1-r)/r,
// never above their own weight, the share Silo's own split now gives them,
// and get the same title query, so an episode looked up by its title still
// comes first (73% in the top 10 either way) while plot matches no longer
// crowd out the meaning results (16, all episodes named with the typed words).
//
// The title query goes out twice: as typed, and with the last word marked as
// finished (a trailing space). Meilisearch treats the last word as a prefix
// still being typed and does not forgive a swapped letter in a short prefix,
// so "breaking bda" matched no title as typed but does as a finished word; the
// as-typed query keeps "breaking ba" finding Breaking Bad while typing. With
// typo tolerance from three letters (bloem_search_typo_tolerance.go) a typo in
// a 3-4 letter word put the right title in the top 10 for 79.1% of searches
// with one query and 86.8% with both.
//
// If Meilisearch rejects the federated request, the original request runs
// unchanged, so the boost never makes a search worse than before.

const bloemTitleBoostWeight = 1.2

// bloemTitleBoostOn switches the title queries on. Bloem's server turns it on
// at startup (bloemWireCoreDependencies in cmd/silo/bloem_main.go); off, every
// search goes out as Silo builds it, which is what Silo's own tests check.
var bloemTitleBoostOn atomic.Bool

// SetBloemTitleBoost turns the title queries on or off for every Meilisearch
// search in this process.
func SetBloemTitleBoost(on bool) { bloemTitleBoostOn.Store(on) }

var bloemTitleBoostAttributes = []string{"title", "original_title", "sort_title", "title_variants"}

// bloemTitleBoostApplies reports whether a query gets the title query: only
// hybrid queries of two or more terms, the same queries that get the meaning
// side at all. That also leaves out the empty-query semantic probe.
func bloemTitleBoostApplies(query string, hybrid *meilisearchHybridRequest) bool {
	return hybrid != nil && len(catalogSearchQueryTerms(query)) >= meilisearchShortHybridMinTerms
}

// bloemTitleQueries returns the title query as typed and with its last word
// finished, weighted bloemTitleBoostWeight times weight.
func bloemTitleQueries(base meilisearchFederatedQuery, weight float64) []meilisearchFederatedQuery {
	typed := strings.TrimSpace(base.Query)
	queries := make([]meilisearchFederatedQuery, 0, 2)
	for _, text := range []string{typed, typed + " "} {
		query := meilisearchFederatedQuery{
			IndexUID:             base.IndexUID,
			Query:                text,
			Filter:               base.Filter,
			AttributesToRetrieve: base.AttributesToRetrieve,
			AttributesToSearchOn: append([]string(nil), bloemTitleBoostAttributes...),
			MatchingStrategy:     "all",
		}
		query.FederationOptions.Weight = weight * bloemTitleBoostWeight
		queries = append(queries, query)
	}
	return queries
}

// withBloemTitleBoost puts the title queries after every query of two or more
// terms in a request that has a meaning side, and weights the keyword-only
// queries down to the keyword share of a hybrid query that mixes keyword and
// meaning. It reports false when no query qualifies.
func withBloemTitleBoost(req meilisearchFederatedSearchRequest) (meilisearchFederatedSearchRequest, bool) {
	var meaning bool
	var mixed *meilisearchHybridRequest
	var termed bool
	heaviest := 0.0
	for _, query := range req.Queries {
		heaviest = max(heaviest, query.FederationOptions.Weight)
		if query.Hybrid != nil {
			meaning = true
			if query.Hybrid.SemanticRatio < 1 && mixed == nil {
				mixed = query.Hybrid
			}
		}
		if len(catalogSearchQueryTerms(query.Query)) >= meilisearchShortHybridMinTerms {
			termed = true
		}
	}
	if !meaning || !termed {
		return req, false
	}
	// A meaning-only query (ratio 1) leaves Silo's keyword queries weighted
	// by its own split; only a mixing hybrid query rescales them.
	keywordShare := 1.0
	if mixed != nil && mixed.SemanticRatio > 0.5 {
		keywordShare = (1 - mixed.SemanticRatio) / mixed.SemanticRatio
	}
	queries := make([]meilisearchFederatedQuery, 0, 3*len(req.Queries))
	for _, query := range req.Queries {
		titles := bloemTitleQueries(query, heaviest)
		if query.Hybrid == nil {
			query.FederationOptions.Weight *= keywordShare
		}
		queries = append(queries, query)
		if len(catalogSearchQueryTerms(query.Query)) >= meilisearchShortHybridMinTerms {
			queries = append(queries, titles...)
		}
	}
	req.Queries = queries
	return req, true
}

// bloemTitleBoostSearch runs a single-index hybrid search as a federated
// search with the title query. handled is false when the request does not
// qualify or Meilisearch rejected the federated form; the caller then runs
// the request as it was.
func (c *meilisearchClient) bloemTitleBoostSearch(ctx context.Context, uid string, req meilisearchSearchRequest) (meilisearchSearchResponse, bool, error) {
	if !bloemTitleBoostOn.Load() || !bloemTitleBoostApplies(req.Query, req.Hybrid) {
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
		Queries:    append([]meilisearchFederatedQuery{main}, bloemTitleQueries(main, main.FederationOptions.Weight)...),
	})
}

// bloemTitleBoostFederatedSearch adds the title queries to a federated search
// whose hybrid query qualifies, such as a search that mixes episodes and
// media.
func (c *meilisearchClient) bloemTitleBoostFederatedSearch(ctx context.Context, req meilisearchFederatedSearchRequest) (meilisearchSearchResponse, bool, error) {
	if !bloemTitleBoostOn.Load() {
		return meilisearchSearchResponse{}, false, nil
	}
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
