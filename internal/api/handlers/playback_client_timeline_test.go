package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestClientTimelineAcceptedReceiptKeepsBothClocks(t *testing.T) {
	timeline, err := playback.NewClientPlaybackTimelineV3("book", "edition", []playback.ClientPlaybackPartV3{{FileID: 1, DurationSeconds: 600}, {FileID: 2, DurationSeconds: 400}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	binding := playback.InitialActivationBindingV3{ClientTimeline: timeline, Progress: userstore.PlaybackProgressSample{DurationSeconds: 1000, Hints: userstore.VersionHints{FileID: 2}}, Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}, Scope: userstore.PlaybackProgressScope{ProfileID: "p", SessionID: uuid.NewString(), MediaItemID: "book"}, Fence: userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}, IntentID: uuid.NewString(), AdmissionID: uuid.NewString()}
	sample, err := binding.ClientTimelineSample(timeline.TimelineID, 2, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &userstore.PlaybackProgressReceipt{Fence: binding.Fence, Sample: sample}
	accepted, err := ClientTimelineAcceptedProgress(binding, receipt)
	if err != nil || accepted.Position != 30 || accepted.ItemPosition == nil || *accepted.ItemPosition != 630 || accepted.Sequence != 2 || accepted.TimelineID != timeline.TimelineID {
		t.Fatalf("receipt %+v %v", accepted, err)
	}
	receipt.Fence.OwnerID = uuid.NewString()
	if _, err := ClientTimelineAcceptedProgress(binding, receipt); err == nil {
		t.Fatal("foreign receipt accepted")
	}
}
func TestClientTimelineLegacyStartRefusesBeforeEffects(t *testing.T) {
	handler := &PlaybackHandler{}
	out := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/v1/playback/start", nil)
	handler.handleStartPlaybackV3(out, request, []byte(`{"progress_persistence":"client_bound"}`))
	if out.Code != 501 {
		t.Fatalf("legacy admitted new mode: %d %s", out.Code, out.Body.String())
	}
}
