package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestInitialAbortDiagnosticsSafeCorrelation(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := context.WithValue(t.Context(), chimw.RequestIDKey, "request-test")
	secret := "private-media-path?credential=secret"
	cause := fmt.Errorf("%s: %w", secret, os.ErrPermission)
	logInitialAbortV3(ctx, "transcode_prepare", "attempt-test", "session-test", cause)
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "credential") {
		t.Fatal("diagnostic leaked underlying error")
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"stage": "transcode_prepare", "reason": "permission_denied", "request_id": "request-test", "playback_attempt_id": "attempt-test", "playback_session_id": "session-test"} {
		if record[key] != want {
			t.Fatalf("%s = %v, want %s", key, record[key], want)
		}
	}
	if !errors.Is(cause, os.ErrPermission) {
		t.Fatal("cause changed")
	}
}

func TestInitialAbortReasonClassification(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{playback.ErrFrozenTranscodePolicyChanged, "frozen_policy_changed"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"},
		{os.ErrNotExist, "not_found"}, {playback.ErrInitialActivationConflictV3, "activation_conflict"},
		{playback.ErrInitialActivationInvalidV3, "activation_invalid"},
		{playback.ErrInitialActivationUnavailableV3, "activation_unavailable"},
		{errors.New("private dependency response"), "dependency_failure"},
	} {
		if got := initialAbortReasonV3(fmt.Errorf("wrapped: %w", tc.cause)); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}

func TestInitialAbortPreparationStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte("private credential media path"))
	}))
	defer server.Close()
	card := executorTransportTestCard()
	card.TranscodeNodeURL = server.URL
	h := &PlaybackHandler{JWTSecret: "private-test-secret"}
	_, err := h.prepareRemoteExecutorRecipeV3(t.Context(), card)
	prepared, ok := errors.AsType[*executorPreparationErrorV3](err)
	if !ok || prepared.class != "http_status" || prepared.status != 422 {
		t.Fatalf("preparation classification: %v", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("worker body leaked")
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logInitialAbortV3(t.Context(), "transcode_prepare", "attempt", "session", err)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["preparation_status"] != float64(422) || record["preparation_class"] != "http_status" {
		t.Fatal("missing preparation status/class")
	}
}
