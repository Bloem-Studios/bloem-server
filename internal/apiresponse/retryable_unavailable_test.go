package apiresponse

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteRetryableUnavailableClampsMatchingHeaderAndBody(t *testing.T) {
	for _, test := range []struct {
		input int
		want  string
	}{
		{input: 0, want: "1"},
		{input: 7, want: "7"},
		{input: 99, want: "30"},
	} {
		recorder := httptest.NewRecorder()
		WriteRetryableUnavailable(recorder, "rollout_pending", "Try again", test.input)
		if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != test.want {
			t.Fatalf("input %d: status/header = %d/%q", test.input, recorder.Code, recorder.Header().Get("Retry-After"))
		}
		if body := recorder.Body.String(); body != `{"error":"rollout_pending","message":"Try again","retry_after":`+test.want+"}\n" {
			t.Fatalf("input %d: body = %q", test.input, body)
		}
	}
}
