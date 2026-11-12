package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloadprepare"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
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
// server and seals a worker grant for exactly the attempt's request when a
// worker prepares it. end revokes the binding and any worker grant.
func (d *NativeVideoDownloads) BeginPreparation(ctx context.Context, artifactID, attemptID string, requester *downloads.Download, file *models.MediaFile) (context.Context, func(), error) {
	if d == nil || d.Service == nil || d.Service.Bindings == nil || d.Prepare == nil || requester == nil || file == nil ||
		!nativeVideoFileSupported(file) || requester.MediaFileID != file.ID || requester.UserID <= 0 || artifactID == "" || requester.ArtifactID != artifactID {
		return nil, nil, catalog.ErrItemNotFound
	}
	transport, ok := transcodenode.NativePreparationTransport(attemptID)
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
		logSession: playback.DownloadPrepareLogSessionID(artifactID), secret: d.Secret}
	return playback.WithContextMediaHost(ctx, host), func() { host.end(ctx) }, nil
}

// nativeVideoPreparation is the context-scoped media host of one download
// preparation attempt.
type nativeVideoPreparation struct {
	service    *NativeVideoService
	session    *playback.Session
	file       *models.MediaFile
	attemptID  string
	logSession string
	secret     func() string
}

var _ playback.ContextWorkerStartSealer = (*nativeVideoPreparation)(nil)

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

// SealWorkerStart grants the worker at nodeURL this attempt's exact
// preparation request, once it shows it can read native storage.
func (p *nativeVideoPreparation) SealWorkerStart(ctx context.Context, nodeURL string, body []byte) error {
	var request downloadprepare.Request
	if err := json.Unmarshal(body, &request); err != nil || request.InputPath != p.file.FilePath || request.ArtifactID != p.attemptID {
		return storagesource.ErrReferenceConflict
	}
	grants := p.service.WorkerGrants
	if !grants.Enabled() || p.secret == nil {
		return nativeVideoUnavailable()
	}
	node, err := p.service.WorkerNativeNode(ctx, nodeURL, p.secret())
	if err != nil {
		return err
	}
	return grants.PrepareDownload(ctx, node, p.service, p.session, p.session.ID, request)
}

// end revokes the attempt's binding and worker grant. It outlives the job's
// cancellation, so a stopped job still withdraws its authority.
func (p *nativeVideoPreparation) end(ctx context.Context) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var err error
	if p.service.WorkerGrants.Enabled() {
		err = p.service.WorkerGrants.RevokeTransport(cleanup, p.session.ID)
	}
	err = errors.Join(err, p.service.Bindings.RevokeSession(cleanup, p.session.ID))
	if err != nil {
		slog.WarnContext(cleanup, "revoking a download preparation's native source failed", "component", "native_video", "error", err)
	}
}
