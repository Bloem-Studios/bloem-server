package jellycompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestLegacyAdmissionReleasedBeforeStreamingBytes(t *testing.T) {
	released := false
	dst := httptest.NewRecorder()
	w := &legacyAdmissionResponseWriter{ResponseWriter: dst, release: func() { released = true }}
	w.WriteHeader(http.StatusOK)
	if !released {
		t.Fatal("media response retained the database gate")
	}
	if _, err := w.Write([]byte("media")); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if dst.Body.String() != "media" || !dst.Flushed {
		t.Fatal("streaming response changed")
	}
	if w.Unwrap() != dst {
		t.Fatal("connection controls lost underlying writer")
	}
}

type refusedLegacyProvider struct {
	userstore.UserStoreProvider
	called bool
}

func (p *refusedLegacyProvider) AcquireLegacyPlaybackAdmission(ctx context.Context, account int) (context.Context, func(), error) {
	p.called = account == 7
	return ctx, nil, userstore.ErrPlaybackSourceUnbound
}
func TestCompatLegacyCreationAndReconstructionRefuseAdmittedAccount(t *testing.T) {
	provider := &refusedLegacyProvider{}
	handler := &PlaybackHandler{storeProvider: provider}
	// Refusal must precede every playback-store, session-manager or executor
	// access, including when the caller is trying to reconstruct an old session.
	_, err := handler.ensureUpstreamPlayback(t.Context(), &Session{StreamAppUserID: 7}, "old-play", PlaybackMediaSource{}, "direct")
	if !provider.called || !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("legacy reconstruction: %v", err)
	}
}
