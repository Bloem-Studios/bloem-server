package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

// An unclassified Live TV failure (a playback-bridge error carrying an ffmpeg
// stderr tail, a pgx error naming tables) must answer a generic 500: the
// detail belongs in the server log, not in the client response.
func TestLiveTVErrorDoesNotLeakInternalDetail(t *testing.T) {
	internal := fmt.Errorf("start channel session: %w",
		errors.New(`ffmpeg exited: stderr tail: [http @ 0x1] http://10.0.0.5:5004/auto/v1?token=s3cret 403; ERROR: relation "live_tv_sessions" (SQLSTATE 42P01)`))

	for name, write := range map[string]func(http.ResponseWriter, error){
		"request-aware": func(w http.ResponseWriter, err error) {
			writeLiveTVRequestError(w, httptest.NewRequest(http.MethodPost, "/x", nil), err)
		},
		"legacy": writeLiveTVError,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			write(rec, internal)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			body := rec.Body.String()
			for _, leak := range []string{"ffmpeg", "s3cret", "10.0.0.5", "SQLSTATE", "live_tv_sessions", "start channel session"} {
				if strings.Contains(body, leak) {
					t.Fatalf("response leaks %q: %s", leak, body)
				}
			}
		})
	}
}

// Typed Live TV errors keep their existing status and message mapping.
func TestLiveTVErrorKeepsTypedMappings(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{livetv.ErrNotFound, http.StatusNotFound},
		{livetv.ErrInvalidArgument, http.StatusBadRequest},
		{livetv.ErrLimitExceeded, http.StatusConflict},
		{livetv.ErrNoTuner, http.StatusConflict},
		{livetv.ErrNotImplemented, http.StatusNotImplemented},
		{livetv.ErrPeerUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeLiveTVRequestError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), tc.err)
		if rec.Code != tc.code {
			t.Fatalf("%v: status = %d, want %d", tc.err, rec.Code, tc.code)
		}
		if tc.err != livetv.ErrPeerUnavailable && !strings.Contains(rec.Body.String(), tc.err.Error()) {
			t.Fatalf("%v: body %s lost the typed message", tc.err, rec.Body.String())
		}
	}
}
