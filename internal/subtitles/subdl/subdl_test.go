package subdl

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/subtitles"
)

func TestSearchCanonicalizesProviderLanguages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"subtitles":[{"lang":"English","language":"EN","url":"/english"},{"lang":"Arabic","url":"/arabic"},{"lang":"Brazilian Portuguese","url":"/portuguese"},{"lang":"German","language":"deu","url":"/german"},{"lang":"Unknown provider value","url":"/unknown"}]}`))
	}))
	defer server.Close()
	results, err := New(Config{BaseURL: server.URL, APIKey: "synthetic"}).Search(t.Context(), subtitles.SearchRequest{Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"en", "ar", "pt", "de", "Unknown provider value"}
	if len(results) != len(want) {
		t.Fatalf("got %d results", len(results))
	}
	for i, code := range want {
		if results[i].Language != code {
			t.Errorf("result %d language = %q, want %q", i, results[i].Language, code)
		}
	}
}
