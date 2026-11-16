package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativeVideoPrepare opens file for a download preparation under the
// authority of requester, a download waiting for artifactID. It returns that
// authority, which every later read rechecks, and the opened file.
type NativeVideoPrepare func(ctx context.Context, requester *downloads.Download, artifactID string, file *models.MediaFile) (NativeVideoAuthority, NativeVideoFile, error)

var _ downloads.NativePreparation = (*NativeVideoDownloads)(nil)

// BeginPreparation binds one download preparation attempt to the source, as
// a playback start binds a playback session: the attempt's UUID names a
// binding sealed with the requester's authority, and every read reopens the
// source through it, rechecking the authority, the binding, the download and
// the source. The returned context carries the source for PrepareFile on this
// server, and the binding a core-access transcode node's grant reads through. end revokes the binding.
func (d *NativeVideoDownloads) BeginPreparation(ctx context.Context, artifactID, attemptID string, requester *downloads.Download, file *models.MediaFile) (context.Context, func(), error) {
	if d == nil || d.Service == nil || d.Service.Bindings == nil || d.Prepare == nil || requester == nil || file == nil ||
		!nativeVideoFileSupported(file) || requester.MediaFileID != file.ID || requester.UserID <= 0 || artifactID == "" || requester.ArtifactID != artifactID {
		return nil, nil, catalog.ErrItemNotFound
	}
	transport, ok := nativePreparationTransport(attemptID)
	if !ok {
		return nil, nil, storagesource.ErrReferenceConflict
	}
	service := d.Service
	session := &playback.Session{ID: transport, UserID: requester.UserID, ProfileID: requester.ProfileID, MediaFileID: file.ID, StartedAt: time.Now().UTC()}
	var authority NativeVideoAuthority
	opened, value, err := service.openWith(ctx, file, func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
		var opened NativeVideoFile
		var err error
		authority, opened, err = d.Prepare(ctx, requester, artifactID, file)
		return opened, err
	})
	if err != nil {
		return nil, nil, err
	}
	err = opened.Reauthorize(ctx)
	_ = opened.Close()
	if err != nil {
		return nil, nil, err
	}
	if authority.UserID != session.UserID || authority.ProfileID != session.ProfileID || authority.DownloadID != requester.ID || authority.ArtifactID != artifactID || !time.Now().Before(authority.ExpiresAt) {
		return nil, nil, storagesource.ErrReferenceConflict
	}
	value.SessionID, value.UserID, value.ProfileID, value.Authority = session.ID, session.UserID, session.ProfileID, authority
	if err := service.Bindings.Put(ctx, session.ID, value); err != nil {
		return nil, nil, err
	}
	frozen := *file
	host := &nativeVideoPreparation{service: service, session: session, file: &frozen, attemptID: attemptID,
		logSession: playback.DownloadPrepareLogSessionID(artifactID)}
	// A transcode node with core source access may prepare it: its grant reads
	// through this binding (NativeNodeSources).
	prepared := WithNativeNodeGrantSession(playback.WithContextMediaHost(ctx, host), session.ID)
	return prepared, func() { host.end(ctx) }, nil
}

// nativeVideoPreparation is the context-scoped media host of one download
// preparation attempt.
type nativeVideoPreparation struct {
	service    *NativeVideoService
	session    *playback.Session
	file       *models.MediaFile
	attemptID  string
	logSession string
}

func (p *nativeVideoPreparation) Handles(inputPath string) bool { return inputPath == p.file.FilePath }

// ServeDirect is never reached: a preparation serves no HTTP request.
func (p *nativeVideoPreparation) ServeDirect(w http.ResponseWriter, _ *http.Request, _ string) error {
	http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	return catalog.ErrItemNotFound
}

// Input returns the attempt's source for PrepareFile, which runs under the
// job's log session rather than the attempt's binding.
func (p *nativeVideoPreparation) Input(ctx context.Context, inputPath string) (playback.TranscodeInputBinding, error) {
	if inputPath != p.file.FilePath || p.logSession == "" {
		return playback.TranscodeInputBinding{}, storagesource.ErrReferenceConflict
	}
	source, format, err := p.service.InputSource(ctx, p.session, p.file)
	if err != nil {
		return playback.TranscodeInputBinding{}, err
	}
	toBinding := func(opts playback.TranscodeOpts) (playback.TranscodeOpts, error) {
		if opts.SessionID != p.logSession || opts.InputPath != p.file.FilePath {
			return opts, storagesource.ErrReferenceConflict
		}
		opts.SessionID = p.session.ID
		return opts, nil
	}
	adapter := &playback.TranscodeInputSource{Open: func(execution context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		opts, err := toBinding(opts)
		if err != nil {
			return nil, err
		}
		return source.Open(execution, opts)
	}}
	if source.Validate != nil {
		adapter.Validate = func(check context.Context, opts playback.TranscodeOpts) error {
			opts, err := toBinding(opts)
			if err != nil {
				return err
			}
			opts.InputSource, opts.InputFormat = source, format
			return source.Validate(check, opts)
		}
	}
	return playback.TranscodeInputBinding{InputPath: p.file.FilePath, InputFormat: format, SessionID: p.logSession, InputSource: adapter}, nil
}

// end revokes the attempt's binding. It outlives the job's
// cancellation, so a stopped job still withdraws its authority.
func (p *nativeVideoPreparation) end(ctx context.Context) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := p.service.Bindings.RevokeSession(cleanup, p.session.ID); err != nil {
		slog.WarnContext(cleanup, "revoking a download preparation's native source failed", "component", "native_video", "error", err)
	}
}

// nativePreparationTransport is the binding name of a download preparation
// attempt: the UUID that ends the attempt's artifact ID (the artifact queue
// names each attempt "<artifact>-<uuid>").
func nativePreparationTransport(attemptID string) (string, bool) {
	if len(attemptID) < 38 || attemptID[len(attemptID)-37] != '-' {
		return "", false
	}
	transport := attemptID[len(attemptID)-36:]
	if _, err := uuid.Parse(transport); err != nil {
		return "", false
	}
	return transport, true
}
