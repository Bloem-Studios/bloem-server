package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
)

type titleBoostCall struct {
	Path    string
	Request meilisearchFederatedSearchRequest
}

// titleBoostServer records every request and answers /multi-search with
// multiStatus; every other path succeeds.
func titleBoostServer(t *testing.T, multiStatus int) (*meilisearchClient, func() []titleBoostCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []titleBoostCall
	client := newMeilisearchTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req meilisearchFederatedSearchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, titleBoostCall{Path: r.URL.Path, Request: req})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/multi-search" && multiStatus != http.StatusOK {
			w.WriteHeader(multiStatus)
			_, _ = w.Write([]byte(`{"message":"refused","code":"invalid_search_attributes_to_search_on"}`))
			return
		}
		_, _ = w.Write([]byte(`{"hits":[{"content_id":"movie-1"}],"estimatedTotalHits":1}`))
	})
	return client, func() []titleBoostCall {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func hybridSearchRequest(query string) meilisearchSearchRequest {
	return meilisearchSearchRequest{
		Query:                query,
		Offset:               20,
		Limit:                10,
		Filter:               `type != "episode"`,
		AttributesToRetrieve: []string{"content_id"},
		MatchingStrategy:     "frequency",
		Vector:               []float32{1, 0},
		Hybrid:               &meilisearchHybridRequest{Embedder: "silo_recommendations", SemanticRatio: 0.6},
	}
}

func TestHybridSearchAlsoQueriesTitlesWithMoreWeight(t *testing.T) {
	client, calls := titleBoostServer(t, http.StatusOK)
	resp, err := client.Search(context.Background(), "catalog", hybridSearchRequest("funny movie about a dog"))
	if err != nil || len(resp.Hits) != 1 {
		t.Fatalf("Search = %#v, %v", resp, err)
	}
	got := calls()
	if len(got) != 1 || got[0].Path != "/multi-search" {
		t.Fatalf("calls = %#v, want one /multi-search", got)
	}
	req := got[0].Request
	if req.Federation.Offset != 20 || req.Federation.Limit != 10 || len(req.Queries) != 2 {
		t.Fatalf("federated request = %#v", req)
	}
	main, title := req.Queries[0], req.Queries[1]
	if main.Hybrid == nil || main.Hybrid.SemanticRatio != 0.6 || len(main.Vector) != 2 || main.FederationOptions.Weight != 1 ||
		main.MatchingStrategy != "frequency" || main.IndexUID != "catalog" {
		t.Fatalf("hybrid query = %#v", main)
	}
	if title.Hybrid != nil || title.Vector != nil || title.MatchingStrategy != "all" ||
		!slices.Equal(title.AttributesToSearchOn, bloemTitleBoostAttributes) ||
		title.Filter != main.Filter || title.Query != main.Query || title.IndexUID != "catalog" ||
		title.FederationOptions.Weight != bloemTitleBoostWeight {
		t.Fatalf("title query = %#v", title)
	}
}

func TestSearchesWithoutTheMeaningSideStayPlain(t *testing.T) {
	for name, req := range map[string]meilisearchSearchRequest{
		"keyword":         {Query: "funny movie about a dog", Limit: 10},
		"one term":        hybridSearchRequest("shawshank"),
		"empty probe":     hybridSearchRequest(""),
		"stop words only": hybridSearchRequest("and"),
	} {
		t.Run(name, func(t *testing.T) {
			client, calls := titleBoostServer(t, http.StatusOK)
			if _, err := client.Search(context.Background(), "catalog", req); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := calls(); len(got) != 1 || got[0].Path != "/indexes/catalog/search" {
				t.Fatalf("calls = %#v, want one plain search", got)
			}
		})
	}
}

func mixedSearchRequest(ratio float64) meilisearchFederatedSearchRequest {
	media := meilisearchFederatedQuery{IndexUID: "catalog", Query: "show about a chef", Filter: `type != "episode"`,
		Vector: []float32{1}, Hybrid: &meilisearchHybridRequest{Embedder: "e", SemanticRatio: ratio}}
	media.FederationOptions.Weight = 1
	episodes := meilisearchFederatedQuery{IndexUID: "catalog", Query: "show about a chef", Filter: `type = "episode"`}
	episodes.FederationOptions.Weight = 1
	return meilisearchFederatedSearchRequest{
		Federation: meilisearchFederationOptions{Limit: 20},
		Queries:    []meilisearchFederatedQuery{media, episodes},
	}
}

func TestMixedSearchWeighsEpisodesByTheKeywordShareAndQueriesBothTitles(t *testing.T) {
	client, calls := titleBoostServer(t, http.StatusOK)
	if _, err := client.FederatedSearch(context.Background(), mixedSearchRequest(0.6)); err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	got := calls()
	if len(got) != 1 || len(got[0].Request.Queries) != 4 {
		t.Fatalf("calls = %#v, want one request with four queries", got)
	}
	q := got[0].Request.Queries
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	if q[0].Hybrid == nil || !near(q[0].FederationOptions.Weight, 1) {
		t.Fatalf("media query = %#v", q[0])
	}
	if q[1].MatchingStrategy != "all" || q[1].Filter != `type != "episode"` || q[1].Hybrid != nil ||
		!near(q[1].FederationOptions.Weight, bloemTitleBoostWeight) {
		t.Fatalf("media title query = %#v", q[1])
	}
	if q[2].Filter != `type = "episode"` || q[2].MatchingStrategy != "" || !near(q[2].FederationOptions.Weight, 0.4/0.6) {
		t.Fatalf("episode query = %#v, want weight (1-0.6)/0.6", q[2])
	}
	if q[3].MatchingStrategy != "all" || q[3].Filter != `type = "episode"` ||
		!slices.Equal(q[3].AttributesToSearchOn, bloemTitleBoostAttributes) ||
		!near(q[3].FederationOptions.Weight, bloemTitleBoostWeight) {
		t.Fatalf("episode title query = %#v", q[3])
	}
}

func TestEpisodesKeepSilosWeightUpToAnEvenMix(t *testing.T) {
	for _, ratio := range []float64{0.3, 0.5} {
		client, calls := titleBoostServer(t, http.StatusOK)
		if _, err := client.FederatedSearch(context.Background(), mixedSearchRequest(ratio)); err != nil {
			t.Fatalf("FederatedSearch: %v", err)
		}
		if q := calls()[0].Request.Queries; len(q) != 4 || q[2].FederationOptions.Weight != 1 {
			t.Fatalf("ratio %v: queries = %#v, want the episode query at weight 1", ratio, q)
		}
	}
}

func TestFederatedSearchWithoutAMeaningSideStaysAsItIs(t *testing.T) {
	client, calls := titleBoostServer(t, http.StatusOK)
	req := mixedSearchRequest(0.6)
	req.Queries[0].Hybrid, req.Queries[0].Vector = nil, nil
	if _, err := client.FederatedSearch(context.Background(), req); err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if q := calls()[0].Request.Queries; len(q) != 2 || q[1].FederationOptions.Weight != 1 {
		t.Fatalf("queries = %#v, want the request unchanged", q)
	}
}

func TestRejectedTitleBoostFallsBackToTheOriginalRequest(t *testing.T) {
	client, calls := titleBoostServer(t, http.StatusBadRequest)
	if _, err := client.Search(context.Background(), "catalog", hybridSearchRequest("funny movie about a dog")); err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := calls()
	if len(got) != 2 || got[0].Path != "/multi-search" || got[1].Path != "/indexes/catalog/search" {
		t.Fatalf("calls = %#v, want the boosted try, then the plain search", got)
	}
}

func TestTitleBoostDoesNotRetryAServerFailure(t *testing.T) {
	client, calls := titleBoostServer(t, http.StatusServiceUnavailable)
	if _, err := client.Search(context.Background(), "catalog", hybridSearchRequest("funny movie about a dog")); err == nil {
		t.Fatal("Search succeeded, want the server failure")
	}
	if got := calls(); len(got) != 1 {
		t.Fatalf("calls = %#v, want one", got)
	}
}
