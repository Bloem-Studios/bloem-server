package playback

import (
	"math"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestInitialActivationFrozenPolicyValidation(t *testing.T) {
	base := InitialActivationBindingV3{Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}, Scope: userstore.PlaybackProgressScope{ProfileID: "p", SessionID: uuid.NewString(), MediaItemID: "m"}, Fence: userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}, IntentID: uuid.NewString(), AdmissionID: uuid.NewString()}
	if err := base.Validate(); err != nil {
		t.Fatalf("zero template: %v", err)
	}
	for _, change := range []func(*InitialActivationBindingV3){
		func(b *InitialActivationBindingV3) { b.Progress.Sequence = 1 },
		func(b *InitialActivationBindingV3) { b.Progress.PositionSeconds = 1 },
		func(b *InitialActivationBindingV3) { b.Progress.Paused = true },
		func(b *InitialActivationBindingV3) { b.Progress.DurationSeconds = math.NaN() },
		func(b *InitialActivationBindingV3) { b.HistoryIdentityJSON = "{" },
	} {
		b := base
		change(&b)
		if err := b.Validate(); err == nil {
			t.Fatalf("invalid template accepted: %+v", b)
		}
	}
}
