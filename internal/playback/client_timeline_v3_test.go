package playback

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func timelineTestBinding(t *testing.T) InitialActivationBindingV3 {
	t.Helper()
	timeline, err := NewClientPlaybackTimelineV3("book", "edition", []ClientPlaybackPartV3{{FileID: 1, DurationSeconds: 600}, {FileID: 2, DurationSeconds: 400}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	return InitialActivationBindingV3{ClientTimeline: timeline, Progress: userstore.PlaybackProgressSample{DurationSeconds: 1000, Hints: userstore.VersionHints{FileID: 2}}, Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}, Scope: userstore.PlaybackProgressScope{ProfileID: "p", SessionID: uuid.NewString(), MediaItemID: "book"}, Fence: userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}, IntentID: uuid.NewString(), AdmissionID: uuid.NewString()}
}
func TestClientTimelineMappingAndImmutableEnvelope(t *testing.T) {
	b := timelineTestBinding(t)
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, local := range []float64{0, 30, 400} {
		sample, err := b.ClientTimelineSample(b.ClientTimeline.TimelineID, 1, local, false)
		if err != nil || sample.PositionSeconds != 600+local || sample.DurationSeconds != 1000 || sample.Hints != b.Progress.Hints {
			t.Fatalf("sample %+v %v", sample, err)
		}
		back, err := b.ClientTimeline.LocalPosition(sample.PositionSeconds)
		if err != nil || back != local {
			t.Fatalf("inverse %v %v", back, err)
		}
	}
	for _, local := range []float64{-1, 401, math.NaN(), math.Inf(1)} {
		if _, err := b.ClientTimelineSample(b.ClientTimeline.TimelineID, 1, local, false); err == nil {
			t.Fatal("invalid local accepted")
		}
	}
	if _, err := b.ClientTimelineSample(strings.Repeat("a", 64), 1, 20, false); err == nil {
		t.Fatal("changed timeline accepted")
	}
	changed := b
	changed.Progress.PersistenceDisabled = true
	if changed.Validate() == nil {
		t.Fatal("disabled projection accepted")
	}
	changed = b
	changed.Scope.MediaItemID = "other"
	if changed.Validate() == nil {
		t.Fatal("other target accepted")
	}
	changed = b
	changed.Progress.Hints.FileID = 1
	if changed.Validate() == nil {
		t.Fatal("other part accepted")
	}
}
func TestClientTimelineManifestIdentityAndRefusals(t *testing.T) {
	parts := []ClientPlaybackPartV3{{FileID: 1, DurationSeconds: 600}, {FileID: 2, DurationSeconds: 400}}
	first, _ := NewClientPlaybackTimelineV3("book", "edition", parts, 1)
	second, _ := NewClientPlaybackTimelineV3("book", "edition", parts, 2)
	if first.TimelineID != second.TimelineID || first.PartOffsetSeconds != 0 || second.PartOffsetSeconds != 600 {
		t.Fatal("manifest differs by selected part")
	}
	for _, variation := range []string{"order", "duration", "edition"} {
		next := append([]ClientPlaybackPartV3{}, parts...)
		edition := "edition"
		switch variation {
		case "order":
			next[0], next[1] = next[1], next[0]
		case "duration":
			next[0].DurationSeconds++
		case "edition":
			edition = "other"
		}
		got, err := NewClientPlaybackTimelineV3("book", edition, next, 2)
		if err != nil || got.TimelineID == second.TimelineID {
			t.Fatalf("manifest change unbound: %s %v", variation, err)
		}
	}
	for _, parts := range [][]ClientPlaybackPartV3{nil, {{FileID: 1}}, {{FileID: 1, DurationSeconds: math.Inf(1)}}, {{FileID: 1, DurationSeconds: 1}, {FileID: 1, DurationSeconds: 1}}} {
		if _, err := NewClientPlaybackTimelineV3("book", "edition", parts, 1); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	if _, err := NewClientPlaybackTimelineV3("book", "edition", parts, 3); err == nil {
		t.Fatal("nonmember selected")
	}
}
func TestClientTimelineLegacyBindingAndSampleUnchanged(t *testing.T) {
	b := timelineTestBinding(t)
	b.ClientTimeline = ClientPlaybackTimelineV3{}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "client_timeline") {
		t.Fatal("zero timeline changed old binding JSON")
	}
	sample := b.Progress
	sample.Sequence = 1
	sample.PositionSeconds = 30
	old := userstore.PlaybackProgressState{Version: 1, Scope: b.Scope, Fence: b.Fence}
	result, err := userstore.PreparePlaybackProgress(&old, userstore.ApplyPlaybackProgressRequest{Scope: b.Scope, Fence: b.Fence, Sample: sample})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Result.State)
	if err != nil {
		t.Fatal(err)
	}
	var restored userstore.PlaybackProgressState
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	replay, err := userstore.PreparePlaybackProgress(&restored, userstore.ApplyPlaybackProgressRequest{Scope: b.Scope, Fence: b.Fence, Sample: sample})
	if err != nil || replay.Result.Outcome != userstore.PlaybackProgressReplayed {
		t.Fatalf("old receipt replay: %+v %v", replay, err)
	}
}

func TestClientTimelineManifestRejectsAlteredOffsets(t *testing.T) {
	manifest, err := NewClientPlaybackManifestV3("book", "edition", []ClientPlaybackPartV3{{FileID: 1, DurationSeconds: 600}, {FileID: 2, DurationSeconds: 400}})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Parts[1].OffsetSeconds = 599
	if _, err := manifest.SelectFile(2); err == nil {
		t.Fatal("altered authoritative offset accepted")
	}
}
