package transcodenode

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/downloadprepare"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// nativePreparationPath is the download preparation route WrapNativeExecution
// admits native inputs on.
const nativePreparationPath = "/downloads/prepare"

// NativePreparationTransport is the worker transport of a download
// preparation attempt: the UUID that ends the attempt's artifact ID (the API
// names each attempt "<artifact>-<uuid>"). The API seals the attempt's grant
// under it, and the worker finds the grant by it.
func NativePreparationTransport(attemptID string) (string, bool) {
	if len(attemptID) < 38 || attemptID[len(attemptID)-37] != '-' {
		return "", false
	}
	transport := attemptID[len(attemptID)-36:]
	if _, err := uuid.Parse(transport); err != nil {
		return "", false
	}
	return transport, true
}

// nativePreparationCandidate describes a preparation request for a native
// input. It is not authority: the grant sealed for this worker and the
// request's exact body decides. Any other body goes to the ordinary handler,
// which judges it as before.
func nativePreparationCandidate(request *NativeExecutionRequest, body []byte) *NativeExecutionRequest {
	var prepare downloadprepare.Request
	if err := json.Unmarshal(body, &prepare); err != nil || !storagesource.IsNativeLocation(prepare.InputPath) {
		return nil
	}
	request.TransportID, _ = NativePreparationTransport(prepare.ArtifactID)
	request.InputPath, request.Prepare = prepare.InputPath, &prepare
	return request
}

// nativePreparationContext binds the granted source to the preparation. The
// preparation runs under its log session (the job's key), which the grant's
// source does not know, so the source sees the grant's transport instead.
// A preparation without a valid log session has no source: PrepareFile then
// refuses the native input.
func nativePreparationContext(ctx context.Context, prepare *downloadprepare.Request, grant *NativeExecutionGrant) context.Context {
	session := prepare.TranscodeOpts("", "", "", nil).SessionID
	if session == "" {
		return ctx
	}
	source := grant.InputSource
	toGrant := func(opts playback.TranscodeOpts) (playback.TranscodeOpts, error) {
		if opts.SessionID != session || opts.InputPath != grant.InputPath {
			return opts, storagesource.ErrReferenceConflict
		}
		opts.SessionID = grant.TransportID
		return opts, nil
	}
	adapter := &playback.TranscodeInputSource{Open: func(execution context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		opts, err := toGrant(opts)
		if err != nil {
			return nil, err
		}
		return source.Open(execution, opts)
	}}
	if source.Validate != nil {
		adapter.Validate = func(check context.Context, opts playback.TranscodeOpts) error {
			opts, err := toGrant(opts)
			if err != nil {
				return err
			}
			return source.Validate(check, opts)
		}
	}
	return playback.WithTranscodeInputSource(ctx, playback.TranscodeInputBinding{
		InputPath: grant.InputPath, InputFormat: grant.InputFormat, SessionID: session, InputSource: adapter,
	})
}
