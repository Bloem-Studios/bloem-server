package recommendations

import (
	"context"
	"slices"
	"testing"
)

// docEmbedder embeds every text as catalog text.
type docEmbedder struct{ docs []string }

func (f *docEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.docs = append(f.docs, texts...)
	return [][]float32{{1}}, nil
}

// queryAwareEmbedder also embeds search queries in their own form.
type queryAwareEmbedder struct {
	docEmbedder
	queries []string
}

func (f *queryAwareEmbedder) EmbedQueries(_ context.Context, queries []string) ([][]float32, error) {
	f.queries = append(f.queries, queries...)
	return [][]float32{{1}}, nil
}

func TestSearchQueriesUseTheQueryForm(t *testing.T) {
	f := &queryAwareEmbedder{}

	if _, err := embedSearchText(context.Background(), f, "boxer makes a comeback"); err != nil {
		t.Fatalf("embedSearchText: %v", err)
	}

	if !slices.Equal(f.queries, []string{"boxer makes a comeback"}) || len(f.docs) != 0 {
		t.Fatalf("queries %q, docs %q; want the query embedded as a query only", f.queries, f.docs)
	}
}

func TestSearchQueriesFallBackToCatalogForm(t *testing.T) {
	f := &docEmbedder{}

	if _, err := embedSearchText(context.Background(), f, "boxer makes a comeback"); err != nil {
		t.Fatalf("embedSearchText: %v", err)
	}

	if !slices.Equal(f.docs, []string{"boxer makes a comeback"}) {
		t.Fatalf("docs %q; want the query embedded through Embed", f.docs)
	}
}
