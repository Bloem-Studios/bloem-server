package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const nativeVideoGrantLimit = 256 << 10

// NativeVideoWorkerGrantStore holds host admission for one worker transport.
// There is no memory fallback: worker bodies and tokens cannot create source
// authority when the shared control plane is unavailable.
type NativeVideoWorkerGrantStore struct{ client *redis.Client }
type nativeVideoWorkerGrant struct {
	ID          string
	NodeID      int
	TransportID string
	Binding     NativeVideoBinding
	StartDigest string
	Accepted    *playback.RecipeCard
	ExpiresAt   time.Time
}

func NewNativeVideoWorkerGrantStore(client *redis.Client) *NativeVideoWorkerGrantStore {
	return &NativeVideoWorkerGrantStore{client: client}
}

func (s *NativeVideoWorkerGrantStore) Enabled() bool {
	return s != nil && s.client != nil
}
func nativeVideoWorkerKey(node int, transport string) (string, error) {
	if node <= 0 {
		return "", storagesource.ErrReferenceConflict
	}
	if len(transport) < 36 || len(transport) > 128 {
		return "", storagesource.ErrReferenceConflict
	}
	if _, err := uuid.Parse(transport[:36]); err != nil || len(transport) > 36 && transport[36] != '-' {
		return "", storagesource.ErrReferenceConflict
	}
	for _, ch := range transport {
		if ch != '-' && (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return "", storagesource.ErrReferenceConflict
		}
	}
	// Transport IDs are host-generated globally unique identities. Node remains
	// immutable record content, so assigning a transport to another node fails.
	return "bloem:native-video-worker:" + transport, nil
}
func nativeVideoDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > nativeVideoGrantLimit {
		return "", storagesource.ErrReferenceConflict
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
func nativeVideoSameBinding(a, b NativeVideoBinding) bool {
	x, err := nativeVideoDigest(a)
	y, other := nativeVideoDigest(b)
	return err == nil && other == nil && x == y
}
func nativeVideoPortableRecipe(recipe playback.RecipeCard) playback.RecipeCard {
	claims := recipe.ToClaims()
	return playback.RecipeCardFromClaims(&claims)
}
func nativeVideoSameRecipe(a, b playback.RecipeCard) bool {
	x, err := nativeVideoDigest(nativeVideoPortableRecipe(a))
	y, other := nativeVideoDigest(nativeVideoPortableRecipe(b))
	return err == nil && other == nil && x == y
}

// Prepare finishes before the host sends Start. Only the exact typed Start
// may use the prepared record while worker attestation is pending.
func (s *NativeVideoWorkerGrantStore) Prepare(ctx context.Context, node int, service *NativeVideoService, session *playback.Session, start transcodenode.TranscodeStartRequest) error {
	record, err := s.prepare(ctx, node, start.SessionID, start.InputPath, service, session)
	if err != nil {
		return err
	}
	record.StartDigest, err = nativeVideoDigest(start)
	if err != nil {
		return err
	}
	return s.insert(ctx, record)
}

// PrepareRecipe admits progressive remux, which has no worker Start POST.
func (s *NativeVideoWorkerGrantStore) PrepareRecipe(ctx context.Context, node int, service *NativeVideoService, session *playback.Session, recipe playback.RecipeCard) error {
	transport := recipe.TranscodeTransportID
	if transport == "" {
		transport = recipe.SessionID
	}
	record, err := s.prepare(ctx, node, transport, recipe.InputPath, service, session)
	if err != nil {
		return err
	}
	if !nativeVideoRecipeIdentity(record, recipe) {
		return storagesource.ErrReferenceConflict
	}
	canonical := nativeVideoPortableRecipe(recipe)
	record.Accepted = &canonical
	return s.insert(ctx, record)
}
func (s *NativeVideoWorkerGrantStore) prepare(ctx context.Context, node int, transport, path string, service *NativeVideoService, session *playback.Session) (nativeVideoWorkerGrant, error) {
	var record nativeVideoWorkerGrant
	if s == nil || s.client == nil || service == nil || service.Bindings == nil || service.OpenDelegated == nil || service.Files == nil || session == nil {
		return record, nativeVideoUnavailable()
	}
	if _, err := nativeVideoWorkerKey(node, transport); err != nil {
		return record, err
	}
	binding, err := service.Bindings.Get(ctx, session.ID, session.MediaFileID)
	if err != nil {
		return record, err
	}
	location, err := storagesource.CatalogLocation(binding.Identity.Reference.LocationID, binding.Identity.Reference.EntryID)
	if err != nil || location != path || path != binding.Identity.FilePath || binding.SessionID != session.ID || binding.UserID != session.UserID || binding.ProfileID != session.ProfileID || binding.Authority.UserID != binding.UserID || binding.Authority.ProfileID != binding.ProfileID || binding.Info.Revision == "" || binding.Info.Revision != binding.Identity.Reference.Revision || binding.Info.LogicalPath != binding.Identity.Reference.LogicalPath || binding.Format == "" {
		return record, storagesource.ErrReferenceConflict
	}
	expires := binding.Authority.ExpiresAt
	started := session.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if cap := started.Add(playback.MaxTokenTTL); cap.Before(expires) {
		expires = cap
	}
	if !time.Now().Before(expires) {
		return record, errPlaybackSessionEnded
	}
	record = nativeVideoWorkerGrant{ID: uuid.NewString(), NodeID: node, TransportID: transport, Binding: binding, ExpiresAt: expires.UTC()}
	if err := s.checkAuthority(ctx, record, service); err != nil {
		return nativeVideoWorkerGrant{}, err
	}
	return record, nil
}
func (s *NativeVideoWorkerGrantStore) insert(ctx context.Context, record nativeVideoWorkerGrant) error {
	key, err := nativeVideoWorkerKey(record.NodeID, record.TransportID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil || len(data) > nativeVideoGrantLimit {
		return storagesource.ErrReferenceConflict
	}
	ttl := time.Until(record.ExpiresAt)
	if ttl <= 0 {
		return errPlaybackSessionEnded
	}
	err = s.client.SetArgs(ctx, key, data, redis.SetArgs{Mode: "NX", TTL: ttl}).Err()
	if !errors.Is(err, redis.Nil) {
		return err
	}
	prior, _, err := s.get(ctx, record.NodeID, record.TransportID)
	if err != nil {
		return err
	}
	if prior.StartDigest != record.StartDigest || !nativeVideoSameBinding(prior.Binding, record.Binding) || !prior.ExpiresAt.Equal(record.ExpiresAt) || (record.Accepted != nil && (prior.Accepted == nil || !nativeVideoSameRecipe(*prior.Accepted, *record.Accepted))) {
		return storagesource.ErrReferenceConflict
	}
	return nil
}

// Accept seals the attested portable recipe without extending source lifetime.
// Compare-and-set prevents concurrent revocation from resurrecting the record.
func (s *NativeVideoWorkerGrantStore) Accept(ctx context.Context, node int, transport string, recipe playback.RecipeCard) error {
	record, prior, err := s.get(ctx, node, transport)
	if err != nil {
		return err
	}
	if !nativeVideoRecipeIdentity(record, recipe) {
		return storagesource.ErrReferenceConflict
	}
	if record.Accepted != nil {
		if !nativeVideoSameRecipe(*record.Accepted, recipe) {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
	canonical := nativeVideoPortableRecipe(recipe)
	record.Accepted = &canonical
	data, err := json.Marshal(record)
	if err != nil || len(data) > nativeVideoGrantLimit {
		return storagesource.ErrReferenceConflict
	}
	key, _ := nativeVideoWorkerKey(node, transport)
	result, err := s.client.Eval(ctx, `if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
return 1`, []string{key}, prior, data).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return storagesource.ErrReferenceConflict
	}
	return nil
}
func nativeVideoRecipeIdentity(record nativeVideoWorkerGrant, recipe playback.RecipeCard) bool {
	transport := recipe.TranscodeTransportID
	if transport == "" {
		transport = recipe.SessionID
	}
	return recipe.SessionID == record.Binding.SessionID && recipe.UserID == record.Binding.UserID && recipe.ProfileID == record.Binding.ProfileID && recipe.MediaFileID == record.Binding.Identity.FileID && recipe.InputPath == record.Binding.Identity.FilePath && transport == record.TransportID && recipe.RoutingExecutionNodeID == record.NodeID
}
func (s *NativeVideoWorkerGrantStore) Revoke(ctx context.Context, node int, transport string) error {
	record, _, err := s.get(ctx, node, transport)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	key, _ := nativeVideoWorkerKey(record.NodeID, transport)
	return s.client.Eval(ctx, `local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local grant = cjson.decode(current)
if grant.ID == ARGV[1] and grant.NodeID == tonumber(ARGV[2]) then return redis.call('DEL', KEYS[1]) end
return 0`, []string{key}, record.ID, node).Err()
}

// RevokeTransport is the host stop/rollback hook when the session no longer
// retains its worker assignment. The transport key is always server-owned.
func (s *NativeVideoWorkerGrantStore) RevokeTransport(ctx context.Context, transport string) error {
	if s == nil || s.client == nil {
		return nativeVideoUnavailable()
	}
	key, err := nativeVideoWorkerKey(1, transport)
	if err != nil {
		return err
	}
	return s.client.Del(ctx, key).Err()
}
func (s *NativeVideoWorkerGrantStore) get(ctx context.Context, node int, transport string) (nativeVideoWorkerGrant, []byte, error) {
	var record nativeVideoWorkerGrant
	if s == nil || s.client == nil {
		return record, nil, nativeVideoUnavailable()
	}
	key, err := nativeVideoWorkerKey(node, transport)
	if err != nil {
		return record, nil, err
	}
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return record, nil, err
	}
	if len(data) > nativeVideoGrantLimit || json.Unmarshal(data, &record) != nil || record.ID == "" || record.NodeID != node || record.TransportID != transport || !time.Now().Before(record.ExpiresAt) || (record.StartDigest == "" && record.Accepted == nil) || record.ExpiresAt.After(record.Binding.Authority.ExpiresAt) {
		return nativeVideoWorkerGrant{}, nil, storagesource.ErrReferenceConflict
	}
	return record, data, nil
}
func (s *NativeVideoWorkerGrantStore) checkAuthority(ctx context.Context, record nativeVideoWorkerGrant, service *NativeVideoService) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil || service.Bindings == nil || !time.Now().Before(record.ExpiresAt) {
		return errPlaybackSessionEnded
	}
	// StreamDeny caches negative lookups and fails open on Redis errors. Source
	// authority requires a current fail-closed control-plane lookup instead.
	denied, err := s.client.Exists(ctx, playback.StreamDenyKey(record.Binding.SessionID)).Result()
	if err != nil {
		return err
	}
	if denied != 0 {
		return errPlaybackSessionEnded
	}
	current, err := service.Bindings.Get(ctx, record.Binding.SessionID, record.Binding.Identity.FileID)
	if err != nil {
		return err
	}
	if !nativeVideoSameBinding(current, record.Binding) {
		return storagesource.ErrReferenceConflict
	}
	if service.Sessions != nil {
		session, err := service.Sessions.GetSession(record.Binding.SessionID)
		if err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
			return err
		}
		if err == nil && (session == nil || session.ID != record.Binding.SessionID || session.UserID != record.Binding.UserID || session.ProfileID != record.Binding.ProfileID || session.MediaFileID != record.Binding.Identity.FileID) {
			return errPlaybackSessionEnded
		}
	}
	return nil
}
func (s *NativeVideoWorkerGrantStore) check(ctx context.Context, frozen nativeVideoWorkerGrant, service *NativeVideoService) error {
	current, _, err := s.get(ctx, frozen.NodeID, frozen.TransportID)
	if err != nil {
		return err
	}
	if current.ID != frozen.ID || current.StartDigest != frozen.StartDigest || !current.ExpiresAt.Equal(frozen.ExpiresAt) || !nativeVideoSameBinding(current.Binding, frozen.Binding) || (frozen.Accepted != nil && (current.Accepted == nil || !nativeVideoSameRecipe(*current.Accepted, *frozen.Accepted))) {
		return storagesource.ErrReferenceConflict
	}
	return s.checkAuthority(ctx, current, service)
}
func (s *NativeVideoWorkerGrantStore) Resolve(ctx context.Context, request transcodenode.NativeExecutionRequest, service *NativeVideoService) (*transcodenode.NativeExecutionGrant, error) {
	record, _, err := s.get(ctx, request.WorkerID, request.TransportID)
	if err != nil {
		return nil, err
	}
	if request.InputPath != record.Binding.Identity.FilePath || service == nil || service.Files == nil || service.OpenDelegated == nil {
		return nil, storagesource.ErrReferenceConflict
	}
	if request.Start != nil {
		digest, err := nativeVideoDigest(*request.Start)
		if err != nil || digest != record.StartDigest || request.Start.SessionID != record.TransportID || request.Start.InputPath != request.InputPath {
			return nil, storagesource.ErrReferenceConflict
		}
	} else if request.Recipe == nil || record.Accepted == nil || !nativeVideoSameRecipe(*request.Recipe, *record.Accepted) {
		return nil, storagesource.ErrReferenceConflict
	}
	if err := s.checkAuthority(ctx, record, service); err != nil {
		return nil, err
	}
	file, err := service.Files.GetByID(ctx, record.Binding.Identity.FileID)
	if err != nil || file == nil {
		return nil, storagesource.ErrReferenceConflict
	}
	session := &playback.Session{ID: record.Binding.SessionID, UserID: record.Binding.UserID, ProfileID: record.Binding.ProfileID, MediaFileID: record.Binding.Identity.FileID}
	clone := *service
	clone.OpenDelegated = func(openCtx context.Context, current *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		if !nativeVideoSameBinding(binding, record.Binding) {
			return nil, storagesource.ErrReferenceConflict
		}
		if err := s.check(openCtx, record, service); err != nil {
			return nil, err
		}
		lifetime, cancel := context.WithDeadline(openCtx, record.ExpiresAt)
		opened, err := service.OpenDelegated(lifetime, current, binding)
		if err != nil || opened == nil {
			cancel()
			if opened != nil {
				_ = opened.Close()
			}
			if err == nil {
				err = nativeVideoUnavailable()
			}
			return nil, err
		}
		guard := &nativeVideoWorkerFile{NativeVideoFile: opened, ctx: lifetime, cancel: cancel, check: func(check context.Context) error { return s.check(check, record, service) }}
		guard.watchCancellation()
		if err := guard.Reauthorize(lifetime); err != nil {
			_ = guard.Close()
			return nil, err
		}
		return guard, nil
	}
	opened, _, err := clone.OpenDelegatedBound(ctx, session, file)
	if err != nil {
		return nil, err
	}
	err = opened.Reauthorize(ctx)
	closeErr := opened.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	source, format, err := clone.InputSource(ctx, session, file)
	if err != nil {
		return nil, err
	}
	normalize := func(opts playback.TranscodeOpts) (playback.TranscodeOpts, error) {
		if opts.SessionID != record.TransportID || opts.InputPath != record.Binding.Identity.FilePath {
			return opts, storagesource.ErrReferenceConflict
		}
		opts.SessionID = record.Binding.SessionID
		return opts, nil
	}
	adapter := &playback.TranscodeInputSource{Open: func(execution context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		if err := s.check(execution, record, service); err != nil {
			return nil, err
		}
		opts, err := normalize(opts)
		if err != nil {
			return nil, err
		}
		return source.Open(execution, opts)
	}}
	if source.Validate != nil {
		adapter.Validate = func(check context.Context, opts playback.TranscodeOpts) error {
			if err := s.check(check, record, service); err != nil {
				return err
			}
			opts, err := normalize(opts)
			if err != nil {
				return err
			}
			opts.InputSource, opts.InputFormat = source, format
			if err := source.Validate(check, opts); err != nil {
				return err
			}
			return s.check(check, record, service)
		}
	}
	deliveryFile := *file
	openDelivery := func(deliveryCtx context.Context) (transcodenode.NativeDeliveryAuthority, error) {
		opened, actual, err := clone.OpenDelegatedBound(deliveryCtx, session, &deliveryFile)
		if err != nil {
			return nil, err
		}
		if !nativeVideoSameBinding(actual, record.Binding) {
			opened.Cancel()
			return nil, errors.Join(storagesource.ErrReferenceConflict, opened.Close())
		}
		if err := opened.Reauthorize(deliveryCtx); err != nil {
			opened.Cancel()
			return nil, errors.Join(err, opened.Close())
		}
		return opened, nil
	}
	return &transcodenode.NativeExecutionGrant{WorkerID: record.NodeID, PlaybackSessionID: record.Binding.SessionID, TransportID: record.TransportID, InputPath: record.Binding.Identity.FilePath, Revision: record.Binding.Info.Revision, InputFormat: format, InputSource: adapter, ExpiresAt: record.ExpiresAt, OpenDelivery: openDelivery}, nil
}

// Cancellation stops source IO; Close preserves its joined cleanup and host
// admission lifetime. Never release an owner while its provider read is active.
type nativeVideoWorkerFile struct {
	NativeVideoFile
	ctx      context.Context
	cancel   context.CancelFunc
	stop     func() bool
	joined   chan struct{}
	check    func(context.Context) error
	mu       sync.Mutex
	sticky   error
	close    sync.Once
	closeErr error
}

func (f *nativeVideoWorkerFile) watchCancellation() {
	f.joined = make(chan struct{})
	f.stop = context.AfterFunc(f.ctx, func() {
		defer close(f.joined)
		f.NativeVideoFile.Cancel()
	})
}

func (f *nativeVideoWorkerFile) Context() context.Context { return f.ctx }
func (f *nativeVideoWorkerFile) Cancel()                  { f.cancel(); f.NativeVideoFile.Cancel() }
func (f *nativeVideoWorkerFile) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return errors.Join(f.sticky, f.NativeVideoFile.Err())
}
func (f *nativeVideoWorkerFile) fail(err error) error {
	if err != nil {
		f.mu.Lock()
		if f.sticky == nil {
			f.sticky = err
		}
		f.mu.Unlock()
		f.Cancel()
	}
	return err
}
func (f *nativeVideoWorkerFile) Reauthorize(ctx context.Context) error {
	if err := f.Err(); err != nil {
		return err
	}
	if err := f.ctx.Err(); err != nil {
		return f.fail(err)
	}
	if err := f.check(ctx); err != nil {
		return f.fail(err)
	}
	if err := f.NativeVideoFile.Reauthorize(ctx); err != nil {
		return f.fail(err)
	}
	return f.fail(f.check(ctx))
}
func (f *nativeVideoWorkerFile) Read(p []byte) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.NativeVideoFile.Read(p)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		clear(p)
		return 0, terminal
	}
	return n, err
}
func (f *nativeVideoWorkerFile) ReadAt(p []byte, offset int64) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.NativeVideoFile.ReadAt(p, offset)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		clear(p)
		return 0, terminal
	}
	return n, err
}
func (f *nativeVideoWorkerFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		return 0, err
	}
	pos, err := f.NativeVideoFile.Seek(offset, whence)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		return 0, terminal
	}
	return pos, err
}
func (f *nativeVideoWorkerFile) Close() error {
	f.close.Do(func() {
		if f.stop != nil {
			if !f.stop() {
				<-f.joined
			}
		}
		f.Cancel()
		f.closeErr = f.NativeVideoFile.Close()
	})
	return f.closeErr
}
