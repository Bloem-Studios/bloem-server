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
	if ParseHWDeviceSet(opts.HWDevice).Multi() {
		// Select on the executor before publishing its immutable recipe. Use the
		// same backend, presence, verification and active-load policy as launch.
		// This short-lived accounting covers preparation only; actual start
		// counts its process against the frozen single device, without rebalancing.
		opts = normalizeTranscodeOptsContext(ctx, opts)
		device, release := AcquireHWDevice(opts.HWDevice, opts.HWAccel)
		defer release()
		if hwAccelBalancesRenderDevices(opts.HWAccel) {
			if err := hwDeviceStat(device); err != nil {
				return TranscodeOpts{}, err
			}
		}
		opts.HWDevice = device
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
