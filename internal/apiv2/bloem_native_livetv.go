package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

// Live TV's client-facing operations.
//
// These five are what a phone or television actually calls, and they are the
// reason the native surface needed a document at all: Live TV is Bloem's own
// feature, Silo has no livetv package and lists it as a non-goal, so /api/v2
// will never describe it and no upstream contract covers it.
//
// The response bodies reuse the types in internal/livetv rather than restating
// them. Those types are already the client DTO registry's source for the Live TV
// package, so the document and the generated Kotlin and Swift describe one
// shape. Restating them here would create a second spelling to drift.
//
// Registering an operation documents it; it does not move it off chi. These are
// still served by handlers.LiveTVHandler.

// BloemLiveTVCapabilityOutput reports whether this viewer can watch Live TV.
type BloemLiveTVCapabilityOutput struct {
	Body livetv.CapabilityResponse
}

// BloemLiveTVChannelsOutput is the channel lineup this viewer may watch.
type BloemLiveTVChannelsOutput struct {
	Body livetv.ChannelsResponse
}

// BloemLiveTVGuideInput bounds a guide query to a window.
type BloemLiveTVGuideInput struct {
	Start string `query:"start" doc:"RFC 3339 start of the guide window. Defaults to now." required:"false"`
	End   string `query:"end" doc:"RFC 3339 end of the guide window. Defaults to a server-chosen span after start." required:"false"`
}

// BloemLiveTVGuideOutput is the programme guide for the requested window.
type BloemLiveTVGuideOutput struct {
	Body livetv.GuideResponse
}

// BloemLiveTVSessionInput names the channel to tune.
type BloemLiveTVSessionInput struct {
	ChannelID string `path:"channelId" doc:"Channel to tune."`
}

// BloemLiveTVSessionOutput carries the tuner lease and the URLs to play it.
type BloemLiveTVSessionOutput struct {
	Body livetv.SessionStartResponse
}

// BloemLiveTVReleaseInput names the session to release.
type BloemLiveTVReleaseInput struct {
	SessionID string `path:"sessionId" doc:"Session to release."`
}

// BloemLiveTVReleaseOutput is empty: releasing a tuner returns nothing but the
// status. It exists so the operation declares a body-less success rather than
// an unspecified one.
type BloemLiveTVReleaseOutput struct{}

func registerBloemLiveTV(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/livetv/capability", "getBloemLiveTVCapability", "livetv",
			"Whether this build supports Live TV, whether this viewer may use it, and whether a channel is available to tune."),
		Class: ClassProfileScoped,
	}, func(context.Context, *struct{}) (*BloemLiveTVCapabilityOutput, error) {
		return &BloemLiveTVCapabilityOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/livetv/channels", "listBloemLiveTVChannels", "livetv",
			"The channel lineup this viewer may watch."),
		Class: ClassProfileScoped,
	}, func(context.Context, *struct{}) (*BloemLiveTVChannelsOutput, error) {
		return &BloemLiveTVChannelsOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/livetv/guide", "getBloemLiveTVGuide", "livetv",
			"Programme guide for a window."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemLiveTVGuideInput) (*BloemLiveTVGuideOutput, error) {
		return &BloemLiveTVGuideOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("POST", "/livetv/channels/{channelId}/session", "startBloemLiveTVSession", "livetv",
			"Tune a channel and take a tuner lease."),
		// Tuners are finite: a repeated start must not silently take a second
		// lease, so this is not naturally idempotent and says so.
		Class:       ClassProfileScoped,
		RetrySafety: RetrySafetyNonRetryable,
	}, func(context.Context, *BloemLiveTVSessionInput) (*BloemLiveTVSessionOutput, error) {
		return &BloemLiveTVSessionOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("DELETE", "/livetv/sessions/{sessionId}", "releaseBloemLiveTVSession", "livetv",
			"Release a tuner lease. Releasing an already-released session succeeds."),
		Class:       ClassProfileScoped,
		RetrySafety: RetrySafetyNaturalIdempotent,
	}, func(context.Context, *BloemLiveTVReleaseInput) (*BloemLiveTVReleaseOutput, error) {
		return &BloemLiveTVReleaseOutput{}, nil
	})
}
