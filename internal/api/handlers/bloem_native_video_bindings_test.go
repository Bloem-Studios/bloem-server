package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/redis/go-redis/v9"
)

// nativeVideoRedisIO stores the binding store's commands in memory so the real
// Redis encoding (JSON values, SET NX, transactions) runs in every test run.
type nativeVideoRedisIO struct {
	mu     sync.Mutex
	values map[string]string
}

func (*nativeVideoRedisIO) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *nativeVideoRedisIO) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error { return r.apply(cmd) }
}
func (r *nativeVideoRedisIO) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		var first error
		for _, cmd := range cmds {
			if name := cmd.Name(); name == "multi" || name == "exec" {
				continue
			}
			if err := r.apply(cmd); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}
func (r *nativeVideoRedisIO) apply(cmd redis.Cmder) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	args := cmd.Args()
	fail := func(err error) error { cmd.SetErr(err); return err }
	if len(args) < 2 {
		return fail(fmt.Errorf("unexpected command %v", args))
	}
	key := fmt.Sprint(args[1])
	switch cmd.Name() {
	case "set":
		value := args[2]
		var text string
		switch v := value.(type) {
		case []byte:
			text = string(v)
		default:
			text = fmt.Sprint(v)
		}
		for _, arg := range args[3:] {
			if strings.EqualFold(fmt.Sprint(arg), "nx") {
				if _, ok := r.values[key]; ok {
					return fail(redis.Nil)
				}
			}
		}
		r.values[key] = text
		cmd.(*redis.StatusCmd).SetVal("OK")
		return nil
	case "get":
		value, ok := r.values[key]
		if !ok {
			return fail(redis.Nil)
		}
		cmd.(*redis.StringCmd).SetVal(value)
		return nil
	case "exists":
		if _, ok := r.values[key]; ok {
			cmd.(*redis.IntCmd).SetVal(1)
		} else {
			cmd.(*redis.IntCmd).SetVal(0)
		}
		return nil
	default:
		return fail(fmt.Errorf("unexpected command %s", cmd.Name()))
	}
}

func nativeVideoFakeRedis(t *testing.T) *redis.Client {
	t.Helper()
	// The hook answers every command; the address is never dialed.
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", Dialer: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("fake Redis is never dialed")
	}})
	client.AddHook(&nativeVideoRedisIO{values: make(map[string]string)})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// The live failure: provider times carry time.Local (or another zone) and the
// delegation expiry carries a monotonic reading, while the Redis JSON copy is
// plain UTC. Every native start then failed with source_revision_changed.
func TestNativeVideoBindingSurvivesRedisRoundTrip(t *testing.T) {
	clients := map[string]func(*testing.T) *redis.Client{"fake": nativeVideoFakeRedis}
	if os.Getenv("SILO_TEST_REDIS_URL") != "" {
		clients["redis"] = nativeVideoGrantRedis
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			testNativeVideoBindingRedisRoundTrip(t, client(t))
		})
	}
}

func testNativeVideoBindingRedisRoundTrip(t *testing.T, client *redis.Client) {
	s, file, session, identity, _ := nativeVideoFixture(t)
	modified := time.Unix(1_760_000_000, 123_456_789).In(time.FixedZone("provider", 2*60*60))
	open := s.Open
	s.Open = func(ctx context.Context, f *models.MediaFile) (NativeVideoFile, error) {
		opened, err := open(ctx, f)
		if err == nil {
			opened.(*videoFileFixture).info.ModifiedAt = modified
		}
		return opened, err
	}
	s.Bindings = NewNativeVideoBindingStore(client)
	s.Delegation = func(context.Context) (NativeVideoAuthority, error) {
		// time.Now carries a monotonic reading that JSON never preserves.
		return NativeVideoAuthority{UserID: session.UserID, ProfileID: session.ProfileID, PlaybackSessionID: session.ID, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Direct play reopens the provider file and compares it with the stored copy.
	r := httptest.NewRequest(http.MethodGet, "/stream/"+session.ID, nil)
	r.Header.Set("Range", "bytes=3-6")
	w := httptest.NewRecorder()
	if err := s.Serve(w, r, session, file); err != nil {
		t.Fatalf("direct play after Redis round trip: %v", err)
	}
	if w.Code != http.StatusPartialContent || w.Body.String() != "3456" {
		t.Fatalf("direct play %d %q", w.Code, w.Body.String())
	}
	if err := s.Validate(t.Context(), session); err != nil {
		t.Fatalf("validate: %v", err)
	}
	stored, err := s.Bindings.Get(t.Context(), session.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Info.ModifiedAt.Location() != time.UTC || !stored.Info.ModifiedAt.Equal(modified) || stored.Authority.ExpiresAt.Location() != time.UTC {
		t.Fatalf("stored times are not canonical UTC: %v %v", stored.Info.ModifiedAt, stored.Authority.ExpiresAt)
	}

	// A replan with the same revision keeps the sealed descriptor.
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatalf("same-revision replan: %v", err)
	}
	again, err := s.Bindings.Get(t.Context(), session.ID, file.ID)
	if err != nil || !sameNativeVideoBinding(again, stored) {
		t.Fatalf("replan replaced the sealed binding: %v", err)
	}

	// A concurrent start that lost SET NX compares its fresh, unnormalized value.
	fresh := stored
	fresh.Info.ModifiedAt = modified
	fresh.Authority.ExpiresAt = stored.Authority.ExpiresAt.Local().Add(0)
	if err := s.Bindings.Put(t.Context(), session.ID, fresh); err != nil {
		t.Fatalf("idempotent put of an equal binding: %v", err)
	}

	// Progressive and HLS inputs reopen the binding through the delegated path.
	source, format, err := s.InputSource(t.Context(), session, file)
	if err != nil {
		t.Fatal(err)
	}
	input, err := source.Open(t.Context(), playback.TranscodeOpts{InputPath: file.FilePath, SessionID: session.ID, InputFormat: format})
	if err != nil {
		t.Fatalf("transcode input after Redis round trip: %v", err)
	}
	_ = input.Close()

	// A real revision change still conflicts.
	identity.Reference.Revision = "version-2"
	w = httptest.NewRecorder()
	if err := s.Serve(w, httptest.NewRequest(http.MethodGet, "/stream/"+session.ID, nil), session, file); err == nil || w.Body.Len() != 0 {
		t.Fatalf("replaced source served: err=%v bytes=%d", err, w.Body.Len())
	}
}

// Every time.Time in a binding must compare by instant: a future field that
// is compared with == would bring back the Redis 409 failure.
func TestNativeVideoBindingComparesEveryTimeByInstant(t *testing.T) {
	base := NativeVideoBinding{SessionID: "s", UserID: 1, ProfileID: "p", Format: "mov", Info: mediasource.Info{Name: "a", Revision: "r", Size: 1}}
	instant := time.Unix(1_760_000_000, 42)
	now := time.Now()
	local := now.Add(instant.Sub(now)) // the same instant, with a monotonic reading
	zoned := instant.In(time.FixedZone("elsewhere", -5*60*60))
	if !local.Equal(zoned) || local.Location() == zoned.Location() {
		t.Fatal("fixture times must be equal instants with different representations")
	}
	a, b := base, base
	count := setNativeVideoTimes(reflect.ValueOf(&a).Elem(), local) + setNativeVideoTimes(reflect.ValueOf(&b).Elem(), zoned)
	if count == 0 {
		t.Fatal("binding has no time fields to check")
	}
	if !sameNativeVideoBinding(a, b) || !sameNativeVideo(a, b) {
		t.Fatalf("bindings with equal instants differ: %v/%v and %v/%v", a.Info.ModifiedAt, a.Authority.ExpiresAt, b.Info.ModifiedAt, b.Authority.ExpiresAt)
	}
	if normalized := normalizeNativeVideoBinding(a); normalized != normalizeNativeVideoBinding(b) {
		t.Fatalf("normalized bindings differ: %#v", normalized)
	}
	b.Info.ModifiedAt = b.Info.ModifiedAt.Add(time.Nanosecond)
	if sameNativeVideoBinding(a, b) || sameNativeVideo(a, b) {
		t.Fatal("different modification instants compared equal")
	}
	b = a
	b.Authority.ExpiresAt = b.Authority.ExpiresAt.Add(time.Second)
	if sameNativeVideoBinding(a, b) {
		t.Fatal("different delegation expiries compared equal")
	}
	if !sameNativeVideo(a, b) {
		t.Fatal("source comparison must ignore delegation")
	}
}

// setNativeVideoTimes sets every reachable time.Time, also in nested structs.
func setNativeVideoTimes(v reflect.Value, value time.Time) int {
	if v.Type() == reflect.TypeOf(time.Time{}) {
		v.Set(reflect.ValueOf(value))
		return 1
	}
	count := 0
	if v.Kind() == reflect.Struct {
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				count += setNativeVideoTimes(v.Field(i), value)
			}
		}
	}
	return count
}

func TestNativeVideoMemoryBindingStoreNormalizesTimes(t *testing.T) {
	store := NewNativeVideoBindingStore(nil)
	s, file, session, _, _ := nativeVideoFixture(t)
	opened, value, err := s.open(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Close()
	value.SessionID, value.UserID, value.ProfileID = session.ID, session.UserID, session.ProfileID
	value.Info.ModifiedAt = time.Unix(1_760_000_000, 7)
	value.Authority.ExpiresAt = time.Now().Add(time.Hour)
	if err := store.Put(t.Context(), session.ID, value); err != nil {
		t.Fatal(err)
	}
	utc := value
	utc.Info.ModifiedAt = utc.Info.ModifiedAt.UTC()
	utc.Authority.ExpiresAt = utc.Authority.ExpiresAt.UTC()
	if err := store.Put(t.Context(), session.ID, utc); err != nil {
		t.Fatalf("equal instants conflicted: %v", err)
	}
	got, err := store.Get(t.Context(), session.ID, file.ID)
	if err != nil || got.Info.ModifiedAt.Location() != time.UTC || !sameNativeVideoBinding(got, value) {
		t.Fatalf("get %v %#v", err, got)
	}
}
