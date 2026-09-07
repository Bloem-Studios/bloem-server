package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"slices"
)

// PreparedBoundProgressiveV3 is process-local preparation, never execution authority.
// Its command and source recipe cannot be changed by a subsequent media request.
type PreparedBoundProgressiveV3 struct {
	card           RecipeCard
	output, binary string
	args           []string
}

func (p *PreparedBoundProgressiveV3) Recipe() RecipeCard {
	card := p.card
	card.Executor = cloneExecutorNamespace(card.Executor)
	return card
}

func BoundProgressiveRecipeDigestV3(card RecipeCard) (string, error) {
	data, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// ValidateBoundProgressiveRecipeV3 refuses other delivery families and implicit
// Dolby Vision policies. Routing and all source/timeline fields remain unchanged.
func ValidateBoundProgressiveRecipeV3(c RecipeCard) error {
	if c.Executor == nil || c.Executor.Validate() != nil || c.SessionID == "" || c.TranscodeTransportID == "" || c.InputPath == "" || c.UserID <= 0 || c.ProfileID == "" || c.MediaFileID <= 0 || c.PlayMethod != PlayRemux || c.RoutingWorkload != "remux" {
		return errors.New("bound progressive recipe required")
	}
	local := c.RoutingExecution == "api" && c.RoutingExecutionNodeID == 0 && c.TranscodeNodeURL == ""
	remote := c.RoutingExecution == "transcode" && c.RoutingExecutionNodeID > 0 && c.TranscodeNodeURL != ""
	egress := (c.RoutingEgress == "api" && c.RoutingEgressNodeID == 0) || (c.RoutingEgress == "proxy" && c.RoutingEgressNodeID > 0)
	if (!local && !remote) || !egress || math.IsNaN(c.SeekSeconds) || math.IsInf(c.SeekSeconds, 0) || c.SeekSeconds < 0 || math.IsNaN(c.TotalDuration) || math.IsInf(c.TotalDuration, 0) || c.TotalDuration <= c.SeekSeconds || math.IsNaN(c.StreamOriginSeconds) || math.IsInf(c.StreamOriginSeconds, 0) || c.StreamOriginSeconds < 0 {
		return errors.New("invalid bound progressive routing or timeline")
	}
	explicitDV := slices.Contains([]RemuxDVMode{RemuxDVPreserveV3, RemuxDVStripToHDR10V3, RemuxDVRejectP7V3}, c.RemuxDVMode)
	nonDV := c.DVProfile == 0 && (c.RemuxDVMode == "" || c.RemuxDVMode == RemuxDVLegacyAutoV3)
	if (!explicitDV && !nonDV) || (c.AudioOnly && c.DVProfile != 0) || c.SubtitleBurnIn || c.ToneMapMode != "" || c.CopyFMP4RecipeVersion != "" || c.CopyVideoMPEGTS || (c.TargetCodecVideo != "" && c.TargetCodecVideo != "copy") || c.AudioTrackIndex < -1 {
		return errors.New("unsupported bound progressive recipe")
	}
	if c.TranscodeAudio && (c.TargetCodecAudio != "aac" || c.TargetAudioChannels <= 0 || c.TargetAudioBitrateKbps <= 0) {
		return errors.New("explicit AAC output required")
	}
	subdir, _ := c.Executor.OutputSubdir()
	if c.OutputSubdir != "" && c.OutputSubdir != subdir {
		return ErrExecutorNamespaceMismatch
	}
	return nil
}

// PrepareBoundProgressiveV3 performs only validation and capability probes. It
// never claims output, starts a media process or changes the captured recipe.
func PrepareBoundProgressiveV3(ctx context.Context, card RecipeCard, outputRoot, ffmpegPath string) (*PreparedBoundProgressiveV3, error) {
	if err := ValidateBoundProgressiveRecipeV3(card); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(card.InputPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("progressive source unavailable")
	}
	output, err := card.Executor.OutputDir(outputRoot)
	if err != nil {
		return nil, err
	}
	binary, err := exec.LookPath(ResolveFFmpegPath(ffmpegPath))
	if err != nil {
		return nil, errors.New("progressive executable unavailable")
	}
	profile, tag := card.DVProfile, false
	switch card.RemuxDVMode {
	case RemuxDVStripToHDR10V3:
		if (profile != 7 && profile != 8) || !supportsDoviRPUFilter(binary) || !sharedDVRPUProbe.CanStrip(ctx, binary, card.InputPath) {
			return nil, errors.New("captured HDR10 strip unavailable")
		}
		profile, tag = 7, true
	case RemuxDVPreserveV3:
		if profile == 7 {
			return nil, errors.New("profile 7 cannot be preserved")
		}
		tag = true
	case RemuxDVRejectP7V3:
		if profile == 7 {
			return nil, errors.New("profile 7 rejected")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	card.Executor = cloneExecutorNamespace(card.Executor)
	return &PreparedBoundProgressiveV3{card: card, output: output, binary: binary, args: buildRemuxArgsWithAudioV3(card.InputPath, "mp4", card.SeekSeconds, card.TranscodeAudio, card.AudioTrackIndex, profile, tag, card.AudioOnly, card.SourceAudioChannels, card.TargetAudioChannels, card.TargetAudioBitrateKbps)}, nil
}
