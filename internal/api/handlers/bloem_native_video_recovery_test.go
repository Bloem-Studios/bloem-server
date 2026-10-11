package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type nativeRecoveryFile struct {
	NativeVideoFile
	err error
}

func (f *nativeRecoveryFile) Reauthorize(context.Context) error { return f.err }

func TestNativeVideoRecoveryAuthorizesBeforeRegisteringSession(t *testing.T) {
	for _, method := range []playback.PlayMethod{playback.PlayDirect, playback.PlayRemux, playback.PlayTranscode} {
		t.Run(string(method), func(t *testing.T) {
			for _, kind := range []string{"allowed", "stopped", "viewer-revoked", "source-revision", "reauthorize-revoked", "missing-binding", "path-mismatch"} {
				t.Run(kind, func(t *testing.T) {
					service, file, original, identity, closed := nativeVideoFixture(t)
					if err := service.Bind(t.Context(), original, file); err != nil {
						t.Fatal(err)
					}
					registry := playback.NewSessionManager(0, 0)
					opens := 0
					service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
						opens++
						if _, err := registry.GetSession(original.ID); !errors.Is(err, playback.ErrSessionNotFound) {
							t.Fatal("source authorization ran after session registration")
						}
						if kind == "stopped" {
							return nil, errPlaybackSessionEnded
						}
						if kind == "viewer-revoked" {
							return nil, errors.New("viewer playback permission withdrawn")
						}
						opened, err := service.Open(ctx, file)
						if err == nil && kind == "reauthorize-revoked" {
							opened = &nativeRecoveryFile{NativeVideoFile: opened, err: errors.New("source permission withdrawn")}
						}
						return opened, err
					}
					h := NewPlaybackHandler(registry, service.Files)
					h.NativeVideo = service
					h.InstallNativeVideoRecovery()
					card := playback.NewDirectRecipeCard(original.ID, original.UserID, original.ProfileID, file.ID)
					card.InputPath, card.PlayMethod = file.FilePath, method
					switch kind {
					case "source-revision":
						identity.Reference.Revision = "replaced-version"
					case "missing-binding":
						service.Bindings = videoBindingFixture{}
					case "path-mismatch":
						card.InputPath = "bloem-storage:another-movie"
					}
					got := h.tm.ReconstructSession(t.Context(), original.ID, original.UserID, card)
					if kind == "allowed" {
						if got == nil || got.PlayMethod != method || opens != 1 || *closed != 2 {
							t.Fatalf("authorized recovery: session=%v opens=%d closed=%d", got, opens, *closed)
						}
						if _, err := registry.GetSession(original.ID); err != nil {
							t.Fatal(err)
						}
						return
					}
					if got != nil {
						t.Fatal("revoked native recipe registered a playback session")
					}
					if _, err := registry.GetSession(original.ID); !errors.Is(err, playback.ErrSessionNotFound) {
						t.Fatalf("refused recovery left a registry entry: %v", err)
					}
				})
			}
		})
	}
}

func TestNativeVideoRecoveryPreservesFilesystemSession(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	h.InstallNativeVideoRecovery()
	card := playback.NewDirectRecipeCard("filesystem-session", 7, "profile", 9)
	card.InputPath = "/library/movie.mp4"
	if got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card); got == nil {
		t.Fatal("filesystem reconstruction changed without a native service")
	}
}

func TestNativeVideoRecoveryRefusesAbsentHostSourceService(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	h.InstallNativeVideoRecovery()
	card := playback.NewDirectRecipeCard("native-session", 7, "profile", 9)
	card.InputPath = "bloem-storage:movie"
	if got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card); got != nil {
		t.Fatal("native recipe reconstructed without host source authority")
	}
	if _, err := registry.GetSession(card.SessionID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("unavailable source left registry entry: %v", err)
	}
}

func TestNativeVideoRecoveryPreservesEarlierSourceAuthorization(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	h.tm.AuthorizeSessionReconstruction = func(context.Context, playback.RecipeCard) error {
		return errors.New("prior host authority refused")
	}
	h.InstallNativeVideoRecovery()
	card := playback.NewDirectRecipeCard("filesystem-session", 7, "profile", 9)
	card.InputPath = "/library/movie.mp4"
	if got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card); got != nil {
		t.Fatal("source adapter replaced an earlier authority denial")
	}
}
