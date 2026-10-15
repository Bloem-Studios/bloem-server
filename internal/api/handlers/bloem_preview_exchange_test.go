package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type previewExchangeFixture struct{}

func (previewExchangeFixture) Exchange(context.Context, preview.ExchangeInput) (preview.ExchangeResult, error) {
	return preview.ExchangeResult{Tokens: &auth.TokenPair{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresIn: 60}, ExpiresAt: time.Now().Add(time.Minute), Item: "movie:1", Ref: "verified-affiliate"}, nil
}
func TestBloemPreviewExchangePreservesVerifiedAttribution(t *testing.T) {
	h := NewBloemPreviewHandler(previewExchangeFixture{}, nil, preview.NewClientBuckets())
	w := httptest.NewRecorder()
	h.HandleCreateSession(w, httptest.NewRequest("POST", "/api/bloem/v1/preview/sessions", strings.NewReader(`{"ticket":"synthetic-ticket"}`)))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 201 || body["ref"] != "verified-affiliate" || body["item"] != "movie:1" {
		t.Fatalf("exchange attribution missing: %d %v", w.Code, body)
	}
}
