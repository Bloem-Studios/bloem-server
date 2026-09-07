package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

const FeatureBoundClientTimelineV3 = "bound_client_timeline"

var ErrClientPlaybackTimelineBusyV3 = errors.New("previous bound playback timeline part is not terminal")

var ErrClientPlaybackTimelineV3 = errors.New("invalid or unavailable bound client playback timeline")

// ClientPlaybackTimelineV3 is captured before sink installation. Its catalog
// manifest never follows later edits, source selection, or another part start.
// The value is comparable so it remains part of exact activation identity.
type ClientPlaybackTimelineV3 struct {
	TimelineID          string  `json:"timeline_id"`
	MediaItemID         string  `json:"media_item_id"`
	FileID              int     `json:"file_id"`
	PartOffsetSeconds   float64 `json:"part_offset_seconds"`
	PartDurationSeconds float64 `json:"part_duration_seconds"`
	DurationSeconds     float64 `json:"duration_seconds"`
}

type ClientPlaybackPartV3 struct {
	FileID          int     `json:"file_id"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// ClientPlaybackTimelineResolverV3 supplies one trusted complete catalog
// snapshot. Discovery and initial admission use this same resolver; admission
// compares the discovered digest before installing the selected part binding.
type ClientPlaybackTimelineResolverV3 interface {
	ResolveClientPlaybackManifest(context.Context, int, string, int) (ClientPlaybackManifestV3, error)
}

type ClientPlaybackManifestPartV3 struct {
	FileID          int     `json:"file_id"`
	OffsetSeconds   float64 `json:"offset_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
}
type ClientPlaybackManifestV3 struct {
	TimelineID      string                         `json:"timeline_id"`
	MediaItemID     string                         `json:"media_item_id"`
	EditionID       string                         `json:"edition_id"`
	DurationSeconds float64                        `json:"duration_seconds"`
	Parts           []ClientPlaybackManifestPartV3 `json:"parts"`
}

func NewClientPlaybackManifestV3(itemID, editionID string, parts []ClientPlaybackPartV3) (ClientPlaybackManifestV3, error) {
	var out ClientPlaybackManifestV3
	if itemID == "" || len(itemID) > 512 || editionID == "" || len(editionID) > 512 || len(parts) == 0 || len(parts) > 4096 {
		return out, ErrClientPlaybackTimelineV3
	}
	seen := make(map[int]bool, len(parts))
	out.Parts = make([]ClientPlaybackManifestPartV3, 0, len(parts))
	for _, part := range parts {
		if part.FileID <= 0 || seen[part.FileID] || !positiveTimelineSeconds(part.DurationSeconds) {
			return ClientPlaybackManifestV3{}, ErrClientPlaybackTimelineV3
		}
		seen[part.FileID] = true
		out.Parts = append(out.Parts, ClientPlaybackManifestPartV3{FileID: part.FileID, OffsetSeconds: out.DurationSeconds, DurationSeconds: part.DurationSeconds})
		previous := out.DurationSeconds
		out.DurationSeconds += part.DurationSeconds
		if !positiveTimelineSeconds(out.DurationSeconds) || out.DurationSeconds <= previous {
			return ClientPlaybackManifestV3{}, ErrClientPlaybackTimelineV3
		}
	}
	manifest := struct {
		ItemID    string                 `json:"item_id"`
		EditionID string                 `json:"edition_id"`
		Parts     []ClientPlaybackPartV3 `json:"parts"`
	}{itemID, editionID, parts}
	data, err := json.Marshal(manifest)
	if err != nil {
		return ClientPlaybackManifestV3{}, err
	}
	digest := sha256.Sum256(data)
	out.TimelineID = hex.EncodeToString(digest[:])
	out.MediaItemID = itemID
	out.EditionID = editionID
	return out, nil
}

func (m ClientPlaybackManifestV3) Validate() error {
	if len(m.Parts) == 0 || len(m.Parts) > 4096 {
		return ErrClientPlaybackTimelineV3
	}
	parts := make([]ClientPlaybackPartV3, len(m.Parts))
	for i, part := range m.Parts {
		parts[i] = ClientPlaybackPartV3{FileID: part.FileID, DurationSeconds: part.DurationSeconds}
	}
	expected, err := NewClientPlaybackManifestV3(m.MediaItemID, m.EditionID, parts)
	if err != nil || m.TimelineID != expected.TimelineID || m.DurationSeconds != expected.DurationSeconds {
		return ErrClientPlaybackTimelineV3
	}
	for i, part := range m.Parts {
		if part != expected.Parts[i] {
			return ErrClientPlaybackTimelineV3
		}
	}
	return nil
}
func (m ClientPlaybackManifestV3) SelectFile(fileID int) (ClientPlaybackTimelineV3, error) {
	if m.Validate() != nil {
		return ClientPlaybackTimelineV3{}, ErrClientPlaybackTimelineV3
	}
	for _, part := range m.Parts {
		if part.FileID == fileID {
			return ClientPlaybackTimelineV3{TimelineID: m.TimelineID, MediaItemID: m.MediaItemID, FileID: fileID, PartOffsetSeconds: part.OffsetSeconds, PartDurationSeconds: part.DurationSeconds, DurationSeconds: m.DurationSeconds}, nil
		}
	}
	return ClientPlaybackTimelineV3{}, ErrClientPlaybackTimelineV3
}
func NewClientPlaybackTimelineV3(itemID, editionID string, parts []ClientPlaybackPartV3, selectedFileID int) (ClientPlaybackTimelineV3, error) {
	manifest, err := NewClientPlaybackManifestV3(itemID, editionID, parts)
	if err != nil {
		return ClientPlaybackTimelineV3{}, err
	}
	return manifest.SelectFile(selectedFileID)
}

func positiveTimelineSeconds(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func (t ClientPlaybackTimelineV3) Validate() error {
	digest, err := hex.DecodeString(t.TimelineID)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != t.TimelineID || t.MediaItemID == "" || len(t.MediaItemID) > 512 || t.FileID <= 0 || t.PartOffsetSeconds < 0 || math.IsNaN(t.PartOffsetSeconds) || math.IsInf(t.PartOffsetSeconds, 0) || !positiveTimelineSeconds(t.PartDurationSeconds) || !positiveTimelineSeconds(t.DurationSeconds) || t.PartOffsetSeconds+t.PartDurationSeconds > t.DurationSeconds {
		return ErrClientPlaybackTimelineV3
	}
	return nil
}
func (t ClientPlaybackTimelineV3) GlobalPosition(timelineID string, local float64) (float64, error) {
	if t.Validate() != nil || timelineID != t.TimelineID || math.IsNaN(local) || math.IsInf(local, 0) || local < 0 || local > t.PartDurationSeconds {
		return 0, ErrClientPlaybackTimelineV3
	}
	return t.PartOffsetSeconds + local, nil
}
func (t ClientPlaybackTimelineV3) LocalPosition(global float64) (float64, error) {
	if t.Validate() != nil || math.IsNaN(global) || math.IsInf(global, 0) || global < t.PartOffsetSeconds || global > t.PartOffsetSeconds+t.PartDurationSeconds {
		return 0, ErrClientPlaybackTimelineV3
	}
	return global - t.PartOffsetSeconds, nil
}

// ClientTimelineSample maps only the captured client-bound envelope. It never
// installs authority or rewrites a sink sample digest format. Callers retain
// the returned sample for an uncertain result, and keep the runtime clock local.
func (b InitialActivationBindingV3) ClientTimelineSample(timelineID string, sequence int64, local float64, paused bool) (userstore.PlaybackProgressSample, error) {
	if b.Validate() != nil || b.ClientTimeline == (ClientPlaybackTimelineV3{}) || sequence <= 0 {
		return userstore.PlaybackProgressSample{}, ErrClientPlaybackTimelineV3
	}
	global, err := b.ClientTimeline.GlobalPosition(timelineID, local)
	if err != nil {
		return userstore.PlaybackProgressSample{}, err
	}
	sample := b.Progress
	sample.Sequence = sequence
	sample.PositionSeconds = global
	sample.Paused = paused
	return sample, nil
}

// BoundClientTimelineEnabledV3 requires runtime opt-in after resolver, clock
// mapping and terminal receipt integration. Schema support alone is not enough.
func BoundClientTimelineEnabledV3(service any) bool {
	runtime, ok := service.(interface{ SupportsBoundClientTimeline() bool })
	return ok && runtime.SupportsBoundClientTimeline()
}

func ValidClientPlaybackTimelineIDV3(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == value
}
