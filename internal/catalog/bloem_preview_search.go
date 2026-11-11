package catalog

import (
	"context"
	"hash/fnv"
	"sync"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// Bloem: preview guests search the Meilisearch index by keyword.
//
// A preview never calls a semantic provider: guests are anonymous and many,
// and a search by meaning costs an embedding call per new phrase. Until
// 2026-11-11 previews therefore searched with PostgreSQL, whose keyword
// ranking is much weaker: "breaking bad" found three titles where the
// Meilisearch index finds every title holding both words (El Camino, Snow
// Globe, No Half Measures and the rest). Previews now use the configured
// Meilisearch provider with search by meaning switched off, so they make no
// embedding call and rank like the words half of a subscriber's search.
//
// Meilisearch keeps a search's ranking in a short-lived session, at most
// searchSessionsPerAccount per account, and every guest shares one account.
// Guests searching at once would push each other's sessions out and break
// scrolling, so a preview request's sessions are kept per login session
// (previewcontext.SearchScope) instead.
//
// Without Meilisearch the preview keeps PostgreSQL keyword search.

// bloemPreviewProviders caches the keyword provider made for each configured
// provider; the configured provider lives for the whole process.
var bloemPreviewProviders sync.Map // *MeilisearchSearchProvider -> *MeilisearchSearchProvider

func bloemPreviewKeywordSearch(ordinary CatalogSearchProvider, repo *ItemRepository) CatalogSearchProvider {
	switch p := ordinary.(type) {
	case *bloemPreviewSearch:
		return p.keyword
	case *MeilisearchSearchProvider:
		if p != nil && p.client != nil {
			return bloemPreviewKeywordProvider(p)
		}
	}
	return NewPostgresSearchProvider(repo)
}

func bloemPreviewKeywordProvider(p *MeilisearchSearchProvider) *MeilisearchSearchProvider {
	if cached, ok := bloemPreviewProviders.Load(p); ok {
		if keyword, ok := cached.(*MeilisearchSearchProvider); ok {
			return keyword
		}
	}
	config := p.config
	config.SemanticEnabled, config.Vectorizer, config.Coverage = false, nil, nil
	keyword := &MeilisearchSearchProvider{
		sessions:    bloemPreviewSessions{owner: p},
		itemRepo:    p.itemRepo,
		stateRepo:   p.stateRepo,
		fallback:    p.fallback,
		client:      p.client,
		config:      config,
		vectorCache: make(map[string]cachedCatalogSearchQueryVector),
	}
	actual, _ := bloemPreviewProviders.LoadOrStore(p, keyword)
	if existing, ok := actual.(*MeilisearchSearchProvider); ok {
		return existing
	}
	return keyword
}

// bloemPreviewSessions stores a preview's ranking sessions in the configured
// provider's store, read at call time because the shared store is attached
// after the provider is built (CatalogSearchService.WithSearchSessionStore).
type bloemPreviewSessions struct{ owner *MeilisearchSearchProvider }

func (s bloemPreviewSessions) put(ctx context.Context, userID int, session searchRankingSession) (string, error) {
	return s.owner.sessions.put(ctx, bloemPreviewSessionOwner(ctx, userID), session)
}

func (s bloemPreviewSessions) get(ctx context.Context, userID int, id string) (*searchRankingSession, error) {
	return s.owner.sessions.get(ctx, bloemPreviewSessionOwner(ctx, userID), id)
}

// bloemPreviewSessionOwner maps a preview login session to its own negative
// key, never a real account ID. A collision only shares eviction between two
// guests: session IDs are random and a session's scope still includes its
// access.
func bloemPreviewSessionOwner(ctx context.Context, userID int) int {
	scope, ok := previewcontext.SearchScope(ctx)
	if !ok {
		return userID
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(scope))
	return -int(h.Sum32()&0x7fffffff) - 1
}
