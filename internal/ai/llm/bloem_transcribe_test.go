package llm

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Bloem: speech-to-text responses that time words but carry no segments,
// as NeMo-Speech.cpp returns, with each word's display text and spacing.

func TestTranscribeBuildsSegmentFromWordsWhenSegmentsAreAbsent(t *testing.T) {
	// NeMo-Speech.cpp shape: verbose_json carries word timings only.
	tr, err := transcribeStub(t, http.StatusOK, `{"task":"transcribe","language":"en","duration":3.0,
			"text":"hi there friend","words":[{"start":0.2,"end":0.5,"word":"hi"},
			{"start":0.6,"end":1.0,"word":"there"},{"start":1.8,"end":2.2,"word":"friend"}]}`)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(tr.Segments) != 1 {
		t.Fatalf("segments = %+v, want one built from the words", tr.Segments)
	}
	seg := tr.Segments[0]
	if seg.Start != 0.2 || seg.End != 2.2 || strings.TrimSpace(seg.Text) == "" || len(seg.Words) != 3 {
		t.Errorf("segment = %+v, want 0.2-2.2 with text and 3 words", seg)
	}
}

func TestTranscribeReadsDisplayAndJoinsPrevious(t *testing.T) {
	// NeMo-Speech.cpp times Japanese per character: the running text (with
	// its punctuation) is in display, and joins_previous drops the space.
	tr, err := transcribeStub(t, http.StatusOK, `{"language":"ja","text":"小林さん。","words":[
			{"start":0.0,"end":0.2,"word":"小"},
			{"start":0.2,"end":0.4,"word":"林","joins_previous":true},
			{"start":0.4,"end":0.6,"word":"さ","joins_previous":true},
			{"start":0.6,"end":0.8,"word":"ん","display":"ん。","joins_previous":true}]}`)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(tr.Segments) != 1 || len(tr.Segments[0].Words) != 4 {
		t.Fatalf("segments = %+v, want one with 4 words", tr.Segments)
	}
	words := tr.Segments[0].Words
	if words[0].JoinsPrevious || !words[1].JoinsPrevious || words[3].Text != "ん。" {
		t.Errorf("words = %+v, want display text and joins_previous carried", words)
	}
	if tr.Segments[0].Text != "小林さん。" {
		t.Errorf("segment text = %q, want the characters joined without spaces", tr.Segments[0].Text)
	}
}

func TestTranscribeEmptyWordsWithoutSegmentsIsNotAnError(t *testing.T) {
	tr, err := transcribeStub(t, http.StatusOK, `{"language":"en","text":"","words":[]}`)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(tr.Segments) != 0 {
		t.Errorf("segments = %v, want empty", tr.Segments)
	}
}

func TestTranscribeRejectsTextWithoutWordsWithoutRetry(t *testing.T) {
	client := NewClient(Config{ASRBaseURL: "https://openrouter.ai/api/v1", ASRModel: "openai/whisper-large-v3"})
	calls := 0
	client.asrHTTP.Transport = retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"text":"Hello","words":[]}`))}, nil
	})
	_, err := client.Transcribe(t.Context(), TranscribeRequest{Filename: "test.wav", Audio: []byte("x")})
	if err == nil || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want rejection without retries", err, calls)
	}
}
