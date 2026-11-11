package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

func previewTestProvider(t *testing.T, vectorizer CatalogSearchQueryVectorizer) *MeilisearchSearchProvider {
	t.Helper()
	provider, err := NewMeilisearchSearchProvider(nil, nil, nil, MeilisearchProviderConfig{
		URL:             "http://127.0.0.1:7700",
		SemanticEnabled: true,
		SemanticRatio:   0.6,
		Vectorizer:      vectorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestPreviewSearchesMeilisearchByKeywordOnly(t *testing.T) {
	vectorizer := &fakeCatalogSearchVectorizer{vector: []float32{0.5, 0.25}}
	ordinary := previewTestProvider(t, vectorizer)
	keyword, ok := bloemPreviewKeywordSearch(ordinary, nil).(*MeilisearchSearchProvider)
	if !ok {
		t.Fatalf("preview provider = %T, want Meilisearch", bloemPreviewKeywordSearch(ordinary, nil))
	}
	if keyword.client != ordinary.client || keyword.config.SemanticEnabled || keyword.config.Vectorizer != nil {
		t.Fatalf("preview provider config = %+v", keyword.config)
	}
	if again := bloemPreviewKeywordSearch(ordinary, nil); again != keyword {
		t.Fatal("preview provider rebuilt for every request")
	}
	if wrapped := BloemPreviewSearch(ordinary, nil).(*bloemPreviewSearch); wrapped.keyword != keyword {
		t.Fatal("watch search uses a different preview provider")
	}
	ctx := previewcontext.With(t.Context())
	resolver := NewCatalogResolver(nil, nil).WithSearchProvider(ordinary).ForPreview(ctx)
	if resolver.searchProvider != keyword {
		t.Fatalf("preview catalog provider = %T", resolver.searchProvider)
	}

	req := CatalogSearchRequest{Query: "funny movie about a dog"}
	if built, _ := keyword.buildMeilisearchSearchRequest(ctx, req); built.Hybrid != nil || built.Vector != nil {
		t.Fatalf("preview request = %+v, want keyword only", built)
	}
	if vectorizer.calls != 0 {
		t.Fatal("preview search embedded its query")
	}
	if built, _ := ordinary.buildMeilisearchSearchRequest(t.Context(), req); built.Hybrid == nil {
		t.Fatal("subscriber search lost its meaning side")
	}
}

func TestPreviewWithoutMeilisearchKeepsPostgres(t *testing.T) {
	for name, ordinary := range map[string]CatalogSearchProvider{
		"postgres":         NewPostgresSearchProvider(nil),
		"unconfigured":     &MeilisearchSearchProvider{},
		"no provider":      nil,
		"nil meilisearch":  (*MeilisearchSearchProvider)(nil),
		"recorded wrapper": &bloemPreviewSearch{keyword: NewPostgresSearchProvider(nil)},
	} {
		if _, ok := bloemPreviewKeywordSearch(ordinary, nil).(*PostgresSearchProvider); !ok {
			t.Fatalf("%s: preview provider = %T, want PostgreSQL", name, bloemPreviewKeywordSearch(ordinary, nil))
		}
	}
}

func TestPreviewGuestsKeepTheirOwnSearchSessions(t *testing.T) {
	keyword := bloemPreviewKeywordSearch(previewTestProvider(t, nil), nil).(*MeilisearchSearchProvider)
	guest := func(session string) context.Context {
		return previewcontext.WithSearchScope(previewcontext.With(t.Context()), session)
	}
	const sharedAccount = 169
	live := time.Now().Add(searchSessionTTL)
	first, err := keyword.sessions.put(guest("guest-a"), sharedAccount, searchRankingSession{IDs: []string{"movie-1"}, ExpiresAt: live})
	if err != nil {
		t.Fatal(err)
	}
	// Another guest on the same account searches more than one account may keep.
	for range searchSessionsPerAccount + 2 {
		if _, err := keyword.sessions.put(guest("guest-b"), sharedAccount, searchRankingSession{ExpiresAt: live}); err != nil {
			t.Fatal(err)
		}
	}
	session, err := keyword.sessions.get(guest("guest-a"), sharedAccount, first)
	if err != nil || session.IDs[0] != "movie-1" {
		t.Fatalf("first guest's session = %+v, %v", session, err)
	}
	if _, err := keyword.sessions.get(guest("guest-b"), sharedAccount, first); err == nil {
		t.Fatal("another guest read the first guest's session")
	}
	if owner := bloemPreviewSessionOwner(guest("guest-a"), sharedAccount); owner >= 0 {
		t.Fatalf("guest session owner = %d, want a negative key", owner)
	}
	if owner := bloemPreviewSessionOwner(t.Context(), sharedAccount); owner != sharedAccount {
		t.Fatalf("request without a login session: owner = %d", owner)
	}
}

func TestPreviewSessionsFollowTheSharedStoreAttachedLater(t *testing.T) {
	ordinary := previewTestProvider(t, nil)
	keyword := bloemPreviewKeywordSearch(ordinary, nil).(*MeilisearchSearchProvider)
	store := &recordingSessionStore{}
	ordinary.sessions = store // as CatalogSearchService.WithSearchSessionStore does after startup
	if _, err := keyword.sessions.put(previewcontext.With(t.Context()), 169, searchRankingSession{}); !errors.Is(err, errRecordedSessionStore) {
		t.Fatalf("put = %v, want it to reach the attached store", err)
	}
	if store.puts != 1 {
		t.Fatalf("attached store saw %d puts", store.puts)
	}
}

var errRecordedSessionStore = errors.New("recorded")

type recordingSessionStore struct{ puts int }

func (s *recordingSessionStore) put(context.Context, int, searchRankingSession) (string, error) {
	s.puts++
	return "", errRecordedSessionStore
}

func (s *recordingSessionStore) get(context.Context, int, string) (*searchRankingSession, error) {
	return nil, errRecordedSessionStore
}
