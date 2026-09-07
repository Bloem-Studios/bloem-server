package notifications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/watchstate"
)

type legacyOriginStore struct {
	userstore.UserStore
	calls int
}

func (s *legacyOriginStore) GetProgress(context.Context, string, string) (*userstore.WatchProgress, error) {
	return nil, nil
}
func (s *legacyOriginStore) SetProgress(ctx context.Context, _, _ string, _, _ float64, _ userstore.ProgressThresholds) error {
	if !userstore.IsLegacyPlaybackWrite(ctx) {
		return errors.New("playback origin missing")
	}
	s.calls++
	return userstore.ErrPlaybackSourceUnbound
}
func TestNotificationWrapperPreservesPlaybackStopOriginAndRefusal(t *testing.T) {
	store := &legacyOriginStore{}
	provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: store}, &System{})
	service := watchstate.NewService(provider)
	_, err := service.RecordPlaybackStop(t.Context(), 1, "profile", "movie", 100, 50, time.Now(), userstore.VersionHints{}, userstore.ProgressThresholds{})
	if !errors.Is(err, userstore.ErrPlaybackSourceUnbound) || store.calls != 1 {
		t.Fatalf("stop origin/refusal: %v calls=%d", err, store.calls)
	}
}

type legacyAdmissionProvider struct {
	userstore.UserStoreProvider
	called bool
}

func (p *legacyAdmissionProvider) AcquireLegacyPlaybackAdmission(ctx context.Context, account int) (context.Context, func(), error) {
	p.called = account == 7
	return ctx, nil, userstore.ErrPlaybackSourceUnbound
}
func TestNotificationWrapperPreservesLegacyAdmissionRefusal(t *testing.T) {
	inner := &legacyAdmissionProvider{}
	wrapped := WrapUserStoreProvider(inner, &System{})
	_, _, err := userstore.AcquireLegacyPlaybackAdmission(t.Context(), wrapped, 7)
	if !inner.called || !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("admission forwarding: %v called=%v", err, inner.called)
	}
}
