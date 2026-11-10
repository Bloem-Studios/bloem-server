package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// recordGeminiTexts answers batchEmbedContents with one vector per request and
// records the text each request carried.
func recordGeminiTexts(t *testing.T, sent *[]string) embeddingTransport {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		var req geminiEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		embeddings := make([]map[string]any, 0, len(req.Requests))
		for _, single := range req.Requests {
			*sent = append(*sent, single.Content.Parts[0].Text)
			embeddings = append(embeddings, map[string]any{"values": []float32{0.6, 0.8}})
		}
		body, err := json.Marshal(map[string]any{"embeddings": embeddings})
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return embeddingHTTPResponse(http.StatusOK, string(body), ""), nil
	}
}

func geminiModelClient(t *testing.T, model string, sent *[]string) *Client {
	t.Helper()
	c := NewClient(ClientConfig{BaseURL: "https://generativelanguage.googleapis.com", Model: model, APIKey: "test-key"})
	c.httpClient.Transport = recordGeminiTexts(t, sent)
	return c
}

func TestGeminiEmbedding2LabelsCatalogText(t *testing.T) {
	var sent []string
	c := geminiModelClient(t, "gemini-embedding-2", &sent)

	if _, err := c.Embed(context.Background(), []string{"Drama movie about a boxer", "Comedy series"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	want := []string{"title: none | text: Drama movie about a boxer", "title: none | text: Comedy series"}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}

func TestGeminiEmbedding2LabelsSearchQueries(t *testing.T) {
	var sent []string
	c := geminiModelClient(t, "gemini-embedding-2", &sent)

	if _, err := c.EmbedQueries(context.Background(), []string{"boxer makes a comeback"}); err != nil {
		t.Fatalf("EmbedQueries: %v", err)
	}

	want := []string{"task: search result | query: boxer makes a comeback"}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}

func TestOtherModelsEmbedTextUnchanged(t *testing.T) {
	for _, model := range []string{"gemini-embedding-001", "gemini-embedding-20"} {
		t.Run(model, func(t *testing.T) {
			var sent []string
			c := geminiModelClient(t, model, &sent)

			if _, err := c.Embed(context.Background(), []string{"Drama movie about a boxer"}); err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if _, err := c.EmbedQueries(context.Background(), []string{"boxer makes a comeback"}); err != nil {
				t.Fatalf("EmbedQueries: %v", err)
			}

			want := []string{"Drama movie about a boxer", "boxer makes a comeback"}
			if !slices.Equal(sent, want) {
				t.Fatalf("sent %q, want %q", sent, want)
			}
		})
	}
}
