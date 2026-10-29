package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Redis stays real; only the selected write fails at the client boundary.
type nativeStopHistoryWriteFailure struct {
	key         string
	transaction bool
}

func (nativeStopHistoryWriteFailure) DialHook(next redis.DialHook) redis.DialHook { return next }
func (f nativeStopHistoryWriteFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !f.transaction && cmd.Name() == "set" && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == f.key {
			return errors.New("injected native authority write failure")
		}
		return next(ctx, cmd)
	}
}
func (f nativeStopHistoryWriteFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if f.transaction {
			for _, cmd := range cmds {
				if len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == f.key {
					return errors.New("injected native binding transaction failure")
				}
			}
		}
		return next(ctx, cmds)
	}
}

// A changed catalog path must not hide an earlier native source grant. Omitting
// the binding-history branch lets stop succeed despite a failed deny SET.
func TestNativeVideoStopHistorySurvivesCatalogChange(t *testing.T) {
	for _, current := range []string{"filesystem", "missing", "no-resolver"} {
		t.Run(current, func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, _, _ := nativeVideoGrantFixture(t, client)
			manager := service.Sessions.(*playback.SessionManager)
			switch current {
			case "filesystem":
				service.Files = testPlaybackFileResolver{file: &models.MediaFile{ID: session.MediaFileID, FilePath: "/fixture/movie.mp4"}}
			case "missing":
				service.Files = testPlaybackFileResolver{}
			case "no-resolver":
				service.Files = nil
			}
			failed := redis.NewClient(client.Options())
			t.Cleanup(func() { _ = failed.Close() })
			failed.AddHook(nativeStopHistoryWriteFailure{key: playback.StreamDenyKey(session.ID)})
			service.Bindings = NewNativeVideoBindingStore(failed)
			h := NewPlaybackHandler(manager, service.Files)
			h.NativeVideo = service
			h.StreamDeny = playback.NewStreamDeny(failed)
			if err := h.stopPlaybackSession(t.Context(), session, PlaybackEndStopped); err == nil {
				t.Fatal("stop discarded native history without durable denial")
			}
			if _, err := manager.GetSession(session.ID); err != nil {
				t.Fatalf("failed denial removed retryable session: %v", err)
			}
			if n, err := client.Exists(t.Context(), playback.StreamDenyKey(session.ID)).Result(); err != nil || n != 0 {
				t.Fatalf("failed denial unexpectedly published authority: %d %v", n, err)
			}
			service.Bindings = NewNativeVideoBindingStore(client)
			h.StreamDeny = playback.NewStreamDeny(client)
			if err := h.stopPlaybackSession(t.Context(), session, PlaybackEndStopped); err != nil {
				t.Fatalf("retry after repaired denial: %v", err)
			}
			if _, err := manager.GetSession(session.ID); !errors.Is(err, playback.ErrSessionNotFound) {
				t.Fatalf("successful stop retained live session: %v", err)
			}
			if !playback.NewStreamDeny(client).Denied(t.Context(), session.ID) {
				t.Fatal("fresh replica did not observe stopped native authority")
			}
		})
	}
}

// Filesystem sessions without a native descriptor retain ordinary best-effort
// stream denial; installing native support must not make their stop durable.
func TestNativeVideoStopHistoryLeavesOrdinaryFilesystemStop(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	manager := playback.NewSessionManager(0, 0)
	session, err := manager.StartSession(1, "profile", 42, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	file := &models.MediaFile{ID: 42, FilePath: "/fixture/ordinary.mp4"}
	failed := redis.NewClient(client.Options())
	t.Cleanup(func() { _ = failed.Close() })
	failed.AddHook(nativeStopHistoryWriteFailure{key: playback.StreamDenyKey(session.ID)})
	service := &NativeVideoService{Bindings: NewNativeVideoBindingStore(failed), Files: testPlaybackFileResolver{file: file}, Sessions: manager}
	h := NewPlaybackHandler(manager, service.Files)
	h.NativeVideo = service
	h.StreamDeny = playback.NewStreamDeny(failed)
	if err := h.stopPlaybackSession(t.Context(), session, PlaybackEndStopped); err != nil {
		t.Fatalf("ordinary filesystem stop required native history: %v", err)
	}
	if _, err := manager.GetSession(session.ID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("ordinary stop retained session: %v", err)
	}
}

func TestNativeVideoStopHistoryRedisPublicationAndExpiry(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	marker := "bloem:native-video-session:" + session.ID
	t.Cleanup(func() { _ = client.Del(context.Background(), marker).Err() })
	history, ok := service.Bindings.(interface {
		HasSession(context.Context, string) (bool, error)
	})
	if !ok {
		t.Fatal("production binding store has no session history")
	}
	if exists, err := history.HasSession(t.Context(), session.ID); err != nil || !exists {
		t.Fatalf("native binding omitted session history: %v %v", exists, err)
	}
	key, _ := nativeVideoBindingKey(session.ID, session.MediaFileID)
	bindingTTL, err := client.PTTL(t.Context(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	markerTTL, err := client.PTTL(t.Context(), marker).Result()
	if err != nil || markerTTL <= 0 || markerTTL > playback.MaxTokenTTL || markerTTL < bindingTTL-time.Second {
		t.Fatalf("history expires before recoverable descriptor: descriptor=%v history=%v err=%v", bindingTTL, markerTTL, err)
	}
	prior, err := service.Bindings.Get(t.Context(), session.ID, session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	changed := prior
	changed.Info.Revision, changed.Identity.Reference.Revision = "replacement", "replacement"
	if err := service.Bindings.Put(t.Context(), session.ID, changed); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("history publication replaced sealed binding: %v", err)
	}
	if current, err := service.Bindings.Get(t.Context(), session.ID, session.MediaFileID); err != nil || current != prior {
		t.Fatalf("rejected binding altered original: %v", err)
	}
	if exists, err := history.HasSession(t.Context(), uuid.NewString()); err != nil || exists {
		t.Fatalf("unrelated session acquired native history: %v %v", exists, err)
	}
	// Model an older descriptor and its marker nearing expiry, then publish a
	// later file for the same session. That file's new TTL must refresh history.
	if err := client.PExpire(t.Context(), key, time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.PExpire(t.Context(), marker, time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	later := prior
	later.Identity.FileID = 43
	laterKey := "bloem:native-video:" + session.ID + ":43"
	t.Cleanup(func() { _ = client.Del(context.Background(), laterKey).Err() })
	if err := service.Bindings.Put(t.Context(), session.ID, later); err != nil {
		t.Fatal(err)
	}
	laterTTL, err := client.PTTL(t.Context(), laterKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	markerTTL, err = client.PTTL(t.Context(), marker).Result()
	if err != nil || markerTTL < time.Hour || markerTTL < laterTTL-time.Second {
		t.Fatalf("history omitted later binding lifetime: descriptor=%v history=%v err=%v", laterTTL, markerTTL, err)
	}
}

// A failed atomic publication cannot leave a recoverable descriptor behind
// without its per-session marker; no global Redis scan repairs that invariant.
func TestNativeVideoStopHistoryFailedPutPublishesNothing(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	value, err := service.Bindings.Get(t.Context(), session.ID, session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	newID := uuid.NewString()
	value.SessionID = newID
	key := "bloem:native-video:" + newID + ":42"
	marker := "bloem:native-video-session:" + newID
	t.Cleanup(func() { _ = client.Del(context.Background(), key, marker).Err() })
	failed := redis.NewClient(client.Options())
	t.Cleanup(func() { _ = failed.Close() })
	failed.AddHook(nativeStopHistoryWriteFailure{key: key, transaction: true})
	if err := NewNativeVideoBindingStore(failed).Put(t.Context(), newID, value); err == nil {
		t.Fatal("failed history transaction reported successful publication")
	}
	if n, err := client.Exists(t.Context(), key, marker).Result(); err != nil || n != 0 {
		t.Fatalf("failed history publication left descriptor/marker: %d %v", n, err)
	}
}

// A server rejection of the marker write must abort the whole publication,
// including the descriptor SET NX that precedes it in the transaction.
func TestNativeVideoStopHistoryRedisMarkerFailureIsAtomic(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	value, err := service.Bindings.Get(t.Context(), session.ID, session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	newID := uuid.NewString()
	value.SessionID = newID
	key := "bloem:native-video:" + newID + ":42"
	marker := "bloem:native-video-session:" + newID
	user := "native-stop-history-" + newID
	if err := client.Do(t.Context(), "ACL", "SETUSER", user, "reset", "on", "nopass", "+@all", "~"+key).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Do(context.Background(), "ACL", "DELUSER", user).Err()
		_ = client.Del(context.Background(), key, marker).Err()
	})
	opts := *client.Options()
	// go-redis omits AUTH when the password is empty. A nopass Redis user
	// accepts any nonempty value; use its public test name to select that user.
	opts.Username, opts.Password = user, user
	limited := redis.NewClient(&opts)
	t.Cleanup(func() { _ = limited.Close() })
	if authenticated, err := limited.Do(t.Context(), "ACL", "WHOAMI").Text(); err != nil || authenticated != user {
		t.Fatalf("limited Redis connection did not select fixture user: %v", err)
	}
	if err := NewNativeVideoBindingStore(limited).Put(t.Context(), newID, value); err == nil {
		t.Fatal("marker rejection reported successful native publication")
	}
	if n, err := client.Exists(t.Context(), key, marker).Result(); err != nil || n != 0 {
		t.Fatalf("marker failure left descriptor/marker: %d %v", n, err)
	}
}

func TestNativeVideoStopHistoryMemoryExpiresAndRevokes(t *testing.T) {
	service, file, session, _, _ := nativeVideoFixture(t)
	store := NewNativeVideoBindingStore(nil)
	service.Bindings = store
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	history, ok := store.(interface {
		HasSession(context.Context, string) (bool, error)
	})
	if !ok {
		t.Fatal("memory binding store has no session history")
	}
	if exists, err := history.HasSession(t.Context(), session.ID); err != nil || !exists {
		t.Fatalf("memory descriptor omitted native history: %v %v", exists, err)
	}
	if err := store.RevokeSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if exists, err := history.HasSession(t.Context(), session.ID); err != nil || exists {
		t.Fatalf("revoked memory descriptor retained history: %v %v", exists, err)
	}
	if _, err := history.HasSession(t.Context(), "invalid-session"); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("invalid session history: %v", err)
	}
	concrete := store.(*nativeVideoBindingStore)
	concrete.mu.Lock()
	key := "bloem:native-video:" + session.ID + ":42"
	concrete.values[key] = nativeVideoStoredBinding{Expires: time.Now().Add(-time.Second)}
	concrete.mu.Unlock()
	if exists, err := history.HasSession(t.Context(), session.ID); err != nil || exists {
		t.Fatalf("expired memory descriptor retained history: %v %v", exists, err)
	}
	if len(concrete.values) != 0 {
		t.Fatal("expired memory history was not collected")
	}
}
