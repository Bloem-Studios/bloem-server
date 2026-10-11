package nativestorage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	selectionPurpose   = "bloem/storage-directory-selection/v1"
	cursorPurpose      = "bloem/storage-directory-cursor/v1"
	cursorStatePurpose = "bloem/storage-directory-cursor-state/v1"
	// Leave room for route, source UUID and HTTP framing within an 8 KiB line.
	MaxBrowseQueryBytes  = 6 << 10
	maxBrowseToken       = 4 << 10
	maxBrowseHistory     = 4096
	maxBrowsePages       = 128
	maxBrowseDepth       = 64
	maxBrowseState       = 256 << 10
	maxStoredBrowseState = 512 << 10
	maxActorBrowseStates = 16
	maxTotalBrowseStates = 128
	maxBrowseLifetime    = 15 * time.Minute
)

// A reference carries identities only. Labels are refreshed by Stat; long
// paths and provider metadata never inflate GET navigation references.
type browseReference struct {
	SourceKey             uuid.UUID `json:"s"`
	ConfigurationRevision int64     `json:"r"`
	InstallationID        int64     `json:"i"`
	Generation            uint64    `json:"g"`
	SourceDigest          string    `json:"b"`
	ActorDigest           string    `json:"a"`
	ExpiresAt             int64     `json:"e"`
	Kind                  string    `json:"k"`
	Ancestry              []string  `json:"p"`
}
type browseCursor struct {
	ReferenceDigest string    `json:"r"`
	StateID         uuid.UUID `json:"s"`
	ExpiresAt       int64     `json:"e"`
	Limit           int       `json:"l"`
}
type browseCursorState struct {
	ReferenceDigest string
	ProviderCursor  string
	Offset          int
	Partial         bool
	PageDigest      string
	SeenIDs         []string
	SeenCursors     []string
	Complete        bool
}

// BrowseCursorStore is cluster-shared expiring pagination evidence. Put stores
// an immutable value and atomically retires replaceKey only after saving it.
// Consume atomically requires live state belonging to actorKey before retiring
// it; missing, expired or consumed evidence returns errBrowseCursorRestart.
// Delete is idempotent cleanup and does not establish continuation ownership.
// Its implementation must bound total/per-actor entries as well as bytes/TTL.
// Pagination evidence never grants source or administrative authority.
type BrowseCursorStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key, actorKey, replaceKey string, data []byte, ttl time.Duration) error
	Consume(ctx context.Context, key, actorKey string) error
	Delete(ctx context.Context, key, actorKey string) error
}
type RedisBrowseCursorStore struct{ client redis.UniversalClient }

var errBrowseCursorRestart = errors.New("browse cursor state no longer retained")

func NewRedisBrowseCursorStore(client redis.UniversalClient) *RedisBrowseCursorStore {
	return &RedisBrowseCursorStore{client: client}
}
func (s *RedisBrowseCursorStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("browse cursor store unavailable")
	}
	data, err := s.client.GetRange(ctx, key, 0, maxStoredBrowseState).Bytes()
	if err != nil {
		return nil, err
	}
	if len(data) > maxStoredBrowseState {
		return nil, errors.New("browse cursor state exceeds bound")
	}
	return data, nil
}

// Hash tags keep keys in one Redis Cluster slot. TIME avoids node-clock drift
// in the shared retention indexes. Scores expire with each immutable value.
const browseStatePutScript = `
local clock=redis.call('TIME')
local now=clock[1]*1000+math.floor(clock[2]/1000)
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now)
local total=redis.call('ZCARD',KEYS[1])
local actor=redis.call('ZCARD',KEYS[2])
if ARGV[5]=='1' then
 if redis.call('EXISTS',KEYS[4])~=1 or not redis.call('ZSCORE',KEYS[1],KEYS[4]) or not redis.call('ZSCORE',KEYS[2],KEYS[4]) then return -1 end
 total=total-1
 actor=actor-1
end
if total>=tonumber(ARGV[3]) or actor>=tonumber(ARGV[4]) then return 0 end
if not redis.call('SET',KEYS[3],ARGV[1],'PX',ARGV[2],'NX') then return 0 end
if ARGV[5]=='1' then
 redis.call('DEL',KEYS[4])
 redis.call('ZREM',KEYS[1],KEYS[4])
 redis.call('ZREM',KEYS[2],KEYS[4])
end
redis.call('ZADD',KEYS[1],now+ARGV[2],KEYS[3])
redis.call('ZADD',KEYS[2],now+ARGV[2],KEYS[3])
redis.call('PEXPIRE',KEYS[1],ARGV[6])
redis.call('PEXPIRE',KEYS[2],ARGV[6])
return 1`
const browseStateDeleteScript = `redis.call('DEL',KEYS[3]);redis.call('ZREM',KEYS[1],KEYS[3]);redis.call('ZREM',KEYS[2],KEYS[3]);return 1`
const browseStateConsumeScript = `
local clock=redis.call('TIME')
local now=clock[1]*1000+math.floor(clock[2]/1000)
local total=redis.call('ZSCORE',KEYS[1],KEYS[3])
local actor=redis.call('ZSCORE',KEYS[2],KEYS[3])
if redis.call('EXISTS',KEYS[3])~=1 or not total or not actor or tonumber(total)<=now or tonumber(actor)<=now then return -1 end
redis.call('DEL',KEYS[3])
redis.call('ZREM',KEYS[1],KEYS[3])
redis.call('ZREM',KEYS[2],KEYS[3])
return 1`
const browseStatePrefix = "bloem:{storage-browse}:"

func browseStateKey(id uuid.UUID) string { return browseStatePrefix + "state:" + id.String() }
func (s *RedisBrowseCursorStore) Put(ctx context.Context, key, actorKey, replaceKey string, data []byte, ttl time.Duration) error {
	if s == nil || s.client == nil || len(data) == 0 || len(data) > maxStoredBrowseState || ttl <= 0 || ttl > maxBrowseLifetime {
		return errors.New("invalid browse cursor state")
	}
	replaceFlag := 0
	if replaceKey != "" {
		replaceFlag = 1
	} else {
		replaceKey = key
	}
	result, err := s.client.Eval(ctx, browseStatePutScript, []string{browseStatePrefix + "all", browseStatePrefix + "actor:" + actorKey, key, replaceKey}, data, ttl.Milliseconds(), maxTotalBrowseStates, maxActorBrowseStates, replaceFlag, maxBrowseLifetime.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result == -1 {
		return errBrowseCursorRestart
	}
	if result != 1 {
		return errors.New("browse cursor retention limit")
	}
	return nil
}
func (s *RedisBrowseCursorStore) Delete(ctx context.Context, key, actorKey string) error {
	if s == nil || s.client == nil {
		return errors.New("browse cursor store unavailable")
	}
	return s.client.Eval(ctx, browseStateDeleteScript, []string{browseStatePrefix + "all", browseStatePrefix + "actor:" + actorKey, key}).Err()
}
func (s *RedisBrowseCursorStore) Consume(ctx context.Context, key, actorKey string) error {
	if s == nil || s.client == nil {
		return errors.New("browse cursor store unavailable")
	}
	result, err := s.client.Eval(ctx, browseStateConsumeScript, []string{browseStatePrefix + "all", browseStatePrefix + "actor:" + actorKey, key}).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errBrowseCursorRestart
	}
	return nil
}

// SetCursorStore is startup-only and reuses the host's existing shared Redis.
// Without it, terminal single-page browsing works; pagination fails closed.
func (b *SourceBrowser) SetCursorStore(store BrowseCursorStore) {
	if b != nil {
		b.cursorStore = store
	}
}
func digestBrowse(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		// An empty digest is invalid evidence, never a hash of partial bytes.
		return ""
	}
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func browseActorKey(a auth.AdminContextClaims) string {
	return digestBrowse(struct {
		AccountID   int
		Incarnation uuid.UUID
	}{a.AccountID, a.AccountIncarnationID})
}
func sameBrowseActor(a, b auth.AdminContextClaims) bool {
	return a.AccountID == b.AccountID && a.AccountIncarnationID == b.AccountIncarnationID && a.SessionID == b.SessionID && a.Scope == b.Scope && a.OrganizationID == b.OrganizationID && a.MembershipID == b.MembershipID && a.PolicyRevision == b.PolicyRevision && a.SecurityRevision == b.SecurityRevision && a.EffectiveAuthority == b.EffectiveAuthority && a.ExpiresAt.Equal(b.ExpiresAt)
}
func safeBrowseText(s string, max int, required bool) bool {
	if (required && strings.TrimSpace(s) == "") || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (b *SourceBrowser) newReference(a auth.AdminContextClaims, source storagesource.SourceConfig, generation uint64) browseReference {
	expiry := b.now().Add(maxBrowseLifetime)
	if a.ExpiresAt.Before(expiry) {
		expiry = a.ExpiresAt
	}
	return browseReference{SourceKey: source.Key, ConfigurationRevision: source.ConfigurationRevision, InstallationID: *source.InstallationID, Generation: generation, SourceDigest: digestBrowse(source), ActorDigest: digestBrowse(a), ExpiresAt: expiry.Unix(), Kind: "directory", Ancestry: []string{source.RootEntryID}}
}
func (b *SourceBrowser) sealBrowse(value any, purpose string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxBrowseToken {
		return "", nativeDomainError("browse_limit_exceeded")
	}
	token, err := b.cipher.Encrypt(string(data), purpose)
	if err != nil {
		return "", nativeDomainError("native_storage_unavailable")
	}
	if len(token) > maxBrowseToken {
		return "", nativeDomainError("browse_limit_exceeded")
	}
	return token, nil
}
func (b *SourceBrowser) openBrowse(token, purpose string, value any) error {
	if len(token) == 0 || len(token) > maxBrowseToken {
		return nativeDomainError("invalid_selection_reference")
	}
	data, err := b.cipher.Decrypt(token, purpose)
	if err != nil {
		return nativeDomainError("invalid_selection_reference")
	}
	if err := json.Unmarshal([]byte(data), value); err != nil {
		return nativeDomainError("invalid_selection_reference")
	}
	return nil
}
func (b *SourceBrowser) validateReference(ref browseReference, actor auth.AdminContextClaims, source storagesource.SourceConfig, generation uint64) error {
	if source.InstallationID == nil || ref.SourceDigest == "" || ref.ActorDigest == "" || ref.Kind != "directory" || ref.SourceKey != source.Key || ref.ConfigurationRevision != source.ConfigurationRevision || ref.InstallationID != *source.InstallationID || ref.Generation != generation || ref.SourceDigest != digestBrowse(source) || ref.ActorDigest != digestBrowse(actor) || ref.ExpiresAt <= b.now().Unix() || ref.ExpiresAt > actor.ExpiresAt.Unix() || len(ref.Ancestry) == 0 || len(ref.Ancestry) > maxBrowseDepth || ref.Ancestry[0] != source.RootEntryID {
		return nativeDomainError("invalid_selection_reference")
	}
	seen := map[string]bool{}
	for _, id := range ref.Ancestry {
		if !nativeOpaqueID(id) || seen[id] {
			return nativeDomainError("invalid_selection_reference")
		}
		seen[id] = true
	}
	return nil
}
func (b *SourceBrowser) directory(ref browseReference, name, path string) (BrowseDirectory, error) {
	if ref.SourceDigest == "" || ref.ActorDigest == "" {
		return BrowseDirectory{}, nativeDomainError("invalid_selection_reference")
	}
	token, err := b.sealBrowse(ref, selectionPurpose)
	if err != nil {
		return BrowseDirectory{}, err
	}
	return BrowseDirectory{SelectionRef: token, Name: name, LogicalPath: path}, nil
}
func (b *SourceBrowser) loadCursor(ctx context.Context, a auth.AdminContextClaims, ref browseReference, token string, limit int) (browseCursor, browseCursorState, error) {
	var cursor browseCursor
	var state browseCursorState
	if err := b.openBrowse(token, cursorPurpose, &cursor); err != nil {
		return cursor, state, err
	}
	if cursor.StateID == uuid.Nil || cursor.ReferenceDigest == "" || cursor.ReferenceDigest != digestBrowse(ref) || cursor.ExpiresAt != ref.ExpiresAt || cursor.ExpiresAt <= b.now().Unix() || cursor.Limit != limit {
		return cursor, state, nativeDomainError("invalid_selection_reference")
	}
	if b.cursorStore == nil {
		return cursor, state, nativeDomainError("browse_restart_required")
	}
	data, err := b.cursorStore.Get(ctx, browseStateKey(cursor.StateID))
	if err != nil || len(data) == 0 || len(data) > maxStoredBrowseState {
		return cursor, state, nativeDomainError("browse_restart_required")
	}
	plain, err := b.cipher.Decrypt(string(data), cursorStatePurpose+":"+cursor.StateID.String())
	if err != nil || len(plain) > maxBrowseState {
		return cursor, state, nativeDomainError("browse_restart_required")
	}
	if err := json.Unmarshal([]byte(plain), &state); err != nil || state.ReferenceDigest != cursor.ReferenceDigest || len(state.SeenIDs) > maxBrowseHistory || len(state.SeenCursors) > maxBrowsePages || state.Offset < 0 || state.Offset > 512 {
		return cursor, state, nativeDomainError("browse_restart_required")
	}
	return cursor, state, nil
}
func (b *SourceBrowser) saveCursor(ctx context.Context, a auth.AdminContextClaims, ref browseReference, state browseCursorState, limit int, previous browseCursor) (string, error) {
	if b.cursorStore == nil {
		return "", nativeDomainError("native_storage_unavailable")
	}
	state.ReferenceDigest = digestBrowse(ref)
	if state.ReferenceDigest == "" {
		return "", nativeDomainError("invalid_selection_reference")
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxBrowseState {
		return "", nativeDomainError("browse_limit_exceeded")
	}
	id := uuid.New()
	encrypted, err := b.cipher.Encrypt(string(data), cursorStatePurpose+":"+id.String())
	if err != nil {
		return "", nativeDomainError("native_storage_unavailable")
	}
	cursor := browseCursor{ReferenceDigest: state.ReferenceDigest, StateID: id, ExpiresAt: ref.ExpiresAt, Limit: limit}
	token, err := b.sealBrowse(cursor, cursorPurpose)
	if err != nil {
		return "", err
	}
	directory, err := b.sealBrowse(ref, selectionPurpose)
	if err != nil {
		return "", err
	}
	if len(directory)+len(token)+128 > MaxBrowseQueryBytes {
		return "", nativeDomainError("browse_limit_exceeded")
	}
	replace := ""
	if previous.StateID != uuid.Nil {
		replace = browseStateKey(previous.StateID)
	}
	ttl := time.Unix(ref.ExpiresAt, 0).Sub(b.now())
	if ttl <= 0 || ttl > maxBrowseLifetime {
		return "", nativeDomainError("invalid_selection_reference")
	}
	if err := b.cursorStore.Put(ctx, browseStateKey(id), browseActorKey(a), replace, []byte(encrypted), ttl); err != nil {
		if errors.Is(err, errBrowseCursorRestart) {
			return "", nativeDomainError("browse_restart_required")
		}
		return "", nativeDomainError("browse_limit_exceeded")
	}
	return token, nil
}
