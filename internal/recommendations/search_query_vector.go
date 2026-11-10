package recommendations

import (
	"context"
	"fmt"
	"strings"
)

// EmbedSearchQuery generates a canonical recommendation-space vector for a
// free-text catalog search query.
func (e *Engine) EmbedSearchQuery(ctx context.Context, query string) ([]float32, error) {
	if e == nil || e.embClient == nil {
		return nil, fmt.Errorf("recommendation embedding client is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if strings.TrimSpace(e.cfg.EmbeddingBaseURL) == "" {
		return nil, fmt.Errorf("recommendation embedding base URL is not configured")
	}
	if strings.TrimSpace(e.cfg.EmbeddingModel) == "" {
		return nil, fmt.Errorf("recommendation embedding model is not configured")
	}
	if err := e.ensureEmbeddingLockConfig(ctx); err != nil {
		return nil, err
	}
	vectors, err := embedSearchText(ctx, e.embClient, query)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("embedding API returned no query vector")
	}
	if err := e.validateQueryEmbeddingLock(ctx, vectors[0]); err != nil {
		return nil, err
	}
	return ensureCanonicalDimensions(vectors[0])
}

// embedSearchText embeds a search query in the query form when the client has
// one (embeddings.Client.EmbedQueries), so a model that takes task labels
// compares it with catalog text as a search.
func embedSearchText(ctx context.Context, client embedder, query string) ([][]float32, error) {
	if queries, ok := client.(interface {
		EmbedQueries(ctx context.Context, queries []string) ([][]float32, error)
	}); ok {
		return queries.EmbedQueries(ctx, []string{query})
	}
	return client.Embed(ctx, []string{query})
}

func (e *Engine) validateQueryEmbeddingLock(ctx context.Context, vector []float32) error {
	lock, err := e.repo.GetEmbeddingLock(ctx)
	if err != nil {
		return fmt.Errorf("load embedding lock: %w", err)
	}
	if lock == nil {
		return nil
	}
	return validateQueryEmbeddingLock(lock, e.cfg.EmbeddingModel, len(vector))
}

func validateQueryEmbeddingLock(lock *EmbeddingLock, model string, sourceDimensions int) error {
	if lock == nil {
		return nil
	}
	return lock.Validate(model, sourceDimensions)
}
