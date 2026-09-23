package playback

import "context"

// hwProbeCallerExpired reports whether ResolveHWAccelWithFFmpegContext's caller
// has already expired. Do not register a detached/coalesced probe after that:
// Linux's backend walk also checks the context, but Darwin reaches the
// VideoToolbox cache directly and would otherwise start work after the result
// can no longer be consumed.
func hwProbeCallerExpired(ctx context.Context) bool {
	return ctx.Err() != nil
}
