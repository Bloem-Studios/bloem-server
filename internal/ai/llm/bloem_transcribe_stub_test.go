package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// bloemTranscribeStub transcribes against a server that answers with status
// and body, for Bloem's NeMo-Speech.cpp transcription tests.
func bloemTranscribeStub(t *testing.T, status int, body string) (*Transcription, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	cfg := chatConfig(srv.URL)
	cfg.ASRModel = "whisper-test"
	cfg.ASRBaseURL, cfg.ASRAPIKey = cfg.BaseURL, cfg.APIKey
	return NewClient(cfg).Transcribe(context.Background(), TranscribeRequest{Filename: "c.wav", Audio: []byte("x")})
}
