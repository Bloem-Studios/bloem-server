package playback

import (
	"context"
	"errors"
)

var ErrFrozenTranscodePolicyChanged = errors.New("frozen executor policy changed")

// PrepareFrozenTranscodeOpts resolves execution policy and validates the source
// before an immutable recipe is published. It does not start playback or claim
// an output namespace. StartTranscode repeats validation before execution.
func PrepareFrozenTranscodeOpts(ctx context.Context, opts TranscodeOpts) (TranscodeOpts, error) {
	if opts.Executor != nil && ParseHWDeviceSet(opts.HWDevice).Multi() {
		return TranscodeOpts{}, ErrFrozenTranscodePolicyChanged
	}
	resolved, err := ResolveToneMapExecutor(ctx, opts)
	if err != nil {
		return TranscodeOpts{}, err
	}
	resolved = normalizeTranscodeOptsContext(ctx, resolved)
	if err := validateToneMapOpts(resolved); err != nil {
		return TranscodeOpts{}, err
	}
	if err := validateToneMapSource(ctx, resolved); err != nil {
		return TranscodeOpts{}, err
	}
	return resolved, nil
}

// checkFrozenTranscodePolicy rejects runtime fallback or device selection that
// changes the execution policy already authorized by an immutable recipe.
func checkFrozenTranscodePolicy(frozen, resolved TranscodeOpts) error {
	if frozen.Executor != nil && (frozen.HWAccel != resolved.HWAccel || frozen.HWDevice != resolved.HWDevice || frozen.SoftwareVideoDecode != resolved.SoftwareVideoDecode) {
		return ErrFrozenTranscodePolicyChanged
	}
	return nil
}
