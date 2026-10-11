package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TranscodeInput owns an authorized immutable source and a private tool argument.
// Run must keep the source alive until its callback has reaped the child, then
// join all source IO and release admission. Close is idempotent. Tool arguments
// must never be placed in durable recipes, diagnostics or client responses.
type TranscodeInput interface {
	Run(func(context.Context, string) error) error
	Close() error
}

// TranscodeInputFactory reopens the same frozen source identity for each child.
// The host supplies current viewer/session/source policy and immutable revision
// checks; a catalog location alone is never authority to open a source. ctx is
// the execution lifetime, detached from the HTTP request that started playback.
type TranscodeInputFactory func(context.Context, TranscodeOpts) (TranscodeInput, error)

// TranscodeInputSource keeps TranscodeOpts comparable while installing a host factory.
type TranscodeInputSource struct {
	Open TranscodeInputFactory
	// Validate checks a frozen byte-affecting recipe against current host facts.
	// It replaces filesystem revision validation only for an installed source.
	Validate func(context.Context, TranscodeOpts) error
}

// TranscodeInputBinding lets a host request adapter authorize execution without
// placing runtime sources in the wire recipe. SessionID is the executor's
// transport/session id and must match the recipe passed to StartTranscode.
type TranscodeInputBinding struct {
	InputPath   string
	InputFormat string
	SessionID   string
	InputSource *TranscodeInputSource
}

type transcodeInputContextKey struct{}

func WithTranscodeInputSource(ctx context.Context, binding TranscodeInputBinding) context.Context {
	return context.WithValue(ctx, transcodeInputContextKey{}, binding)
}

// TranscodeInputSourceMatches is only an input-location check. The host still
// owns viewer/worker/session authorization and continuous source revalidation.
func TranscodeInputSourceMatches(ctx context.Context, inputPath string) bool {
	binding, ok := ctx.Value(transcodeInputContextKey{}).(TranscodeInputBinding)
	return ok && binding.InputPath == inputPath && binding.SessionID != "" && binding.InputSource != nil && binding.InputSource.Open != nil
}

func bindTranscodeInput(ctx context.Context, opts TranscodeOpts) (TranscodeOpts, error) {
	binding, ok := ctx.Value(transcodeInputContextKey{}).(TranscodeInputBinding)
	if !ok {
		return opts, nil
	}
	if binding.InputPath != opts.InputPath || binding.SessionID != opts.SessionID || !TranscodeInputSourceMatches(ctx, opts.InputPath) {
		return opts, fmt.Errorf("source input does not match execution identity")
	}
	opts.InputSource, opts.InputFormat = binding.InputSource, binding.InputFormat
	return opts, nil
}

type transcodeInputOffer struct {
	ctx      context.Context
	argument string
}

type transcodeExecutionInput struct {
	input       TranscodeInput
	childJoined chan error
	joined      chan error
}

type transcodeInputFailure struct{ cause error }

func (*transcodeInputFailure) Error() string   { return "playback source input failed" }
func (e *transcodeInputFailure) Unwrap() error { return e.cause }

func validateTranscodeInput(opts TranscodeOpts) error {
	if opts.InputSource == nil {
		return nil
	}
	if opts.InputSource.Open == nil {
		return fmt.Errorf("source input factory is required")
	}
	switch opts.InputFormat {
	case "mov", "matroska", "webm", "avi", "mpeg", "mpegts", "mp3", "flac", "ogg", "wav", "aac", "ac3", "eac3":
	default:
		return fmt.Errorf("unsupported source input format")
	}
	// The filesystem-bound validators and libass alias do not establish native
	// revision authority. These recipes need their own typed-source adapters.
	if opts.ToneMapMode != "" && opts.InputSource.Validate == nil {
		return fmt.Errorf("source input recipe requires a typed revision adapter")
	}
	return nil
}

func prepareTranscodeInput(ctx context.Context, opts TranscodeOpts) (context.Context, TranscodeOpts, *transcodeExecutionInput, error) {
	if opts.InputSource == nil {
		return ctx, opts, nil, nil
	}
	var err error
	opts, err = prepareSourceSubtitleInput(ctx, opts)
	if err != nil {
		return nil, opts, nil, &transcodeInputFailure{err}
	}
	input, err := opts.InputSource.Open(ctx, opts)
	if err != nil {
		return nil, opts, nil, &transcodeInputFailure{err}
	}
	if input == nil {
		return nil, opts, nil, &transcodeInputFailure{errors.New("missing source input")}
	}
	owner := &transcodeExecutionInput{input: input, childJoined: make(chan error, 1), joined: make(chan error, 1)}
	ready := make(chan transcodeInputOffer, 1)
	go func() {
		owner.joined <- input.Run(func(runCtx context.Context, argument string) error {
			ready <- transcodeInputOffer{runCtx, argument}
			// Even cancellation cannot release admission before cmd.Wait joins.
			return <-owner.childJoined
		})
	}()
	select {
	case offer := <-ready:
		if err := offer.ctx.Err(); err != nil {
			return nil, opts, nil, owner.complete(err)
		}
		if offer.argument == "" {
			return nil, opts, nil, owner.complete(errors.New("missing source argument"))
		}
		opts.InputPath = offer.argument // execution-only copy; never s.opts
		return offer.ctx, opts, owner, nil
	case err := <-owner.joined:
		if err == nil {
			err = errors.New("source input did not invoke consumer")
		}
		return nil, opts, nil, &transcodeInputFailure{errors.Join(err, input.Close())}
	}
}

func (i *transcodeExecutionInput) complete(childErr error) error {
	if i == nil {
		return childErr
	}
	i.childJoined <- childErr
	err := errors.Join(childErr, <-i.joined, i.input.Close())
	if err != nil {
		return &transcodeInputFailure{err}
	}
	return nil
}

func transcodeInputEnvironment() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FFREPORT":
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func configureTranscodeInputCommand(cmd *exec.Cmd, input *transcodeExecutionInput) {
	if input == nil {
		return
	}
	cmd.Env = transcodeInputEnvironment()
	configureTranscodeInputProcess(cmd)
}

func (s *TranscodeSession) sourceInputErrorLocked() error {
	if s.opts.InputSource == nil {
		return nil
	}
	err := s.waitErr
	if err == nil && s.executionInput != nil {
		if input, ok := s.executionInput.input.(interface{ Err() error }); ok {
			err = input.Err()
		}
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrTranscodeFailed, &transcodeInputFailure{err})
}

func sourceCopySeekAnchor(ctx context.Context, opts TranscodeOpts, requested float64) (float64, int, error) {
	if opts.InputSource == nil {
		return ResolveCopySeekAnchor(ctx, opts.FFmpegPath, opts.InputPath, requested, opts.SegmentDuration)
	}
	probeSource := &TranscodeInputSource{Open: func(ctx context.Context, probeOpts TranscodeOpts) (TranscodeInput, error) {
		probeOpts.SessionID = opts.SessionID
		return opts.InputSource.Open(ctx, probeOpts)
	}}
	return ResolveSourceCopySeekAnchor(ctx, opts.FFmpegPath, opts.InputPath, probeSource, opts.InputFormat, requested, opts.SegmentDuration)
}
