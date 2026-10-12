package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type nativeVideoStoredBinding struct {
	Value   NativeVideoBinding
	Expires time.Time
}

// Native binding history outlives catalog path changes and file replacements.
// Stores may expose it without extending the ordinary binding-store contract.
type nativeVideoBindingHistory interface {
	HasSession(context.Context, string) (bool, error)
}

type nativeVideoBindingStore struct {
	redis  *redis.Client
	mu     sync.Mutex
	values map[string]nativeVideoStoredBinding
}

// NewNativeVideoBindingStore keeps revision descriptors alongside durable
// recipes. Without Redis they expire in bounded memory; restart recovery then
// fails closed. Neither backend stores credentials or a tool/provider URL.
func NewNativeVideoBindingStore(client *redis.Client) NativeVideoBindings {
	return &nativeVideoBindingStore{redis: client, values: make(map[string]nativeVideoStoredBinding)}
}
func nativeVideoBindingKey(session string, file int) (string, error) {
	if _, err := uuid.Parse(session); err != nil || file <= 0 {
		return "", storagesource.ErrReferenceConflict
	}
	return "bloem:native-video:" + session + ":" + strconv.Itoa(file), nil
}
func (s *nativeVideoBindingStore) Put(ctx context.Context, session string, value NativeVideoBinding) error {
	value = normalizeNativeVideoBinding(value)
	key, err := nativeVideoBindingKey(session, value.Identity.FileID)
	if err != nil {
		return err
	}
	if value.UserID <= 0 || value.Info.Revision == "" || value.Identity.Reference.Revision != value.Info.Revision {
		return storagesource.ErrReferenceConflict
	}
	if s.redis != nil {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		// Set only once: concurrent starts cannot replace a session's revision.
		// The host-owned session marker is published in the same transaction,
		// after the descriptor so its TTL covers every recoverable file. Each
		// Put refreshes history, while SETNX retains the descriptor's own TTL.
		transaction := s.redis.TxPipeline()
		created := transaction.SetArgs(ctx, key, payload, redis.SetArgs{Mode: "NX", TTL: playback.MaxTokenTTL})
		history := transaction.Set(ctx, "bloem:native-video-session:"+session, "1", playback.MaxTokenTTL)
		_, err = transaction.Exec(ctx)
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if err := history.Err(); err != nil {
			return err
		}
		if err := created.Err(); err == nil {
			return nil
		} else if !errors.Is(err, redis.Nil) {
			return err
		}
		prior, err := s.Get(ctx, session, value.Identity.FileID)
		if err != nil {
			return err
		}
		if !sameNativeVideoBinding(prior, value) {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, stored := range s.values {
		if !stored.Expires.After(now) {
			delete(s.values, key)
		}
	}
	if prior, ok := s.values[key]; ok {
		if !sameNativeVideoBinding(prior.Value, value) {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
	if len(s.values) >= 10000 {
		return nativeVideoUnavailable()
	}
	s.values[key] = nativeVideoStoredBinding{Value: value, Expires: now.Add(playback.MaxTokenTTL)}
	return nil
}
func (s *nativeVideoBindingStore) Get(ctx context.Context, session string, file int) (NativeVideoBinding, error) {
	key, err := nativeVideoBindingKey(session, file)
	if err != nil {
		return NativeVideoBinding{}, err
	}
	if s.redis != nil {
		payload, err := s.redis.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return NativeVideoBinding{}, storagesource.ErrReferenceConflict
		}
		if err != nil {
			return NativeVideoBinding{}, err
		}
		var value NativeVideoBinding
		if err = json.Unmarshal(payload, &value); err != nil {
			return NativeVideoBinding{}, storagesource.ErrReferenceConflict
		}
		if value.Identity.FileID != file || value.Identity.Reference.Revision != value.Info.Revision {
			return NativeVideoBinding{}, storagesource.ErrReferenceConflict
		}
		return normalizeNativeVideoBinding(value), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok || !value.Expires.After(time.Now()) {
		delete(s.values, key)
		return NativeVideoBinding{}, storagesource.ErrReferenceConflict
	}
	return normalizeNativeVideoBinding(value.Value), nil
}

// HasSession checks host-owned binding history without trusting the current
// catalog row. Redis uses one marker; memory scans only its bounded descriptors.
func (s *nativeVideoBindingStore) HasSession(ctx context.Context, session string) (bool, error) {
	if s == nil {
		return false, nativeVideoUnavailable()
	}
	if _, err := uuid.Parse(session); err != nil {
		return false, storagesource.ErrReferenceConflict
	}
	if s.redis != nil {
		exists, err := s.redis.Exists(ctx, "bloem:native-video-session:"+session).Result()
		return exists > 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	prefix := "bloem:native-video:" + session + ":"
	found := false
	for key, stored := range s.values {
		if !stored.Expires.After(now) {
			delete(s.values, key)
		} else if strings.HasPrefix(key, prefix) {
			found = true
		}
	}
	return found, nil
}

// RevokeSession is required before removing a stopped native session. A shared
// tombstone fences every binding and recovery path for the full token lifetime.
// Without Redis, deleting the local descriptors prevents local reconstruction.
func (s *nativeVideoBindingStore) RevokeSession(ctx context.Context, session string) error {
	if s == nil {
		return nativeVideoUnavailable()
	}
	if _, err := uuid.Parse(session); err != nil {
		return storagesource.ErrReferenceConflict
	}
	if s.redis != nil {
		return s.redis.Set(ctx, playback.StreamDenyKey(session), "1", playback.MaxTokenTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := "bloem:native-video:" + session + ":"
	for key := range s.values {
		if strings.HasPrefix(key, prefix) {
			delete(s.values, key)
		}
	}
	return nil
}

// normalizeNativeVideoBinding gives every time in a binding the form a Redis
// JSON round trip produces: UTC and no monotonic clock reading. Providers
// report modification times in time.Local (or another zone) and delegations
// carry time.Now readings, so a stored copy never equals a fresh value under
// ==. Stores normalize on Put and Get; comparisons use sameNativeVideoBinding.
func normalizeNativeVideoBinding(v NativeVideoBinding) NativeVideoBinding {
	v.Info.ModifiedAt = v.Info.ModifiedAt.UTC()
	v.Authority.ExpiresAt = v.Authority.ExpiresAt.UTC()
	return v
}

// sameNativeVideoInfo compares a source snapshot; times compare by instant.
func sameNativeVideoInfo(a, b mediasource.Info) bool {
	return a.Name == b.Name && a.LogicalPath == b.LogicalPath && a.Revision == b.Revision && a.Size == b.Size && a.ModifiedAt.Equal(b.ModifiedAt)
}

// sameNativeVideoAuthority compares sealed delegations; times compare by instant.
func sameNativeVideoAuthority(a, b NativeVideoAuthority) bool {
	expiresA, expiresB := a.ExpiresAt, b.ExpiresAt
	a.ExpiresAt, b.ExpiresAt = time.Time{}, time.Time{}
	return a == b && expiresA.Equal(expiresB)
}

// sameNativeVideoBinding compares a whole sealed binding. Never compare
// bindings, Info or authorities with ==: a stored copy has lost the
// location and monotonic reading of the value it was made from.
func sameNativeVideoBinding(a, b NativeVideoBinding) bool {
	return a.SessionID == b.SessionID && a.UserID == b.UserID && a.ProfileID == b.ProfileID && sameNativeVideoAuthority(a.Authority, b.Authority) && sameNativeVideo(a, b)
}
