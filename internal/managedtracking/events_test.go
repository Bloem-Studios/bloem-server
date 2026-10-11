package managedtracking

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func digestEvent(e *pluginv1.WatchSyncEvent) []byte {
	b, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(e)
	h := sha256.Sum256(b)
	return h[:]
}
func TestEventAuthorityCommittedSourceDigestReplayAndHistoryOverlap(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	ctx := t.Context()
	info, _ := s.GetInfo(ctx, id)
	e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err := s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal(err)
	}
	auth := &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: e.Credential, SecretAttributes: map[string]string{"instance_id": info.InstanceID, "account_id": p.AccountID, "profile_id": p.ProfileID}}}
	event := &pluginv1.WatchSyncEvent{EventId: "completed-fixture", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, PlaybackSessionId: "session", WatchHistoryId: "viewing", OccurredAt: timestamppb.New(time.Now()), Media: &pluginv1.WatchSyncMedia{MediaItemId: "fixture-media", MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "10"}}, DurationSeconds: 100, PositionSeconds: 95, Completed: true}
	record := func(e *pluginv1.WatchSyncEvent) *pluginv1.WatchSyncEvent {
		t.Helper()
		wire, err := s.RecordEvents(ctx, id, auth, []*pluginv1.WatchSyncEvent{e})
		if err != nil || len(wire) != 1 {
			t.Fatalf("record: %v %v", wire, err)
		}
		return wire[0]
	}
	if record(event) != nil {
		t.Fatal("uncommitted event accepted")
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES('viewing',$1,$2,'fixture-media',100,true)`, p.AccountID, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	account, _ := strconv.Atoi(p.AccountID)
	conn, _, _ := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, p.ProfileID)
	if err := repo.UpsertScrobbleSession(ctx, watchsync.ScrobbleEvent{PlaybackSessionID: "session", MediaItemID: "fixture-media", HistoryID: "viewing", PositionSeconds: 95, DurationSeconds: 100, Completed: true}, conn.ID, "stop_confirming"); err != nil {
		t.Fatal(err)
	}
	wire := record(event)
	if wire == nil || wire.EventId == event.EventId || !strings.HasPrefix(wire.EventId, wireEventPrefix) {
		t.Fatalf("committed event not vouched for under a host ID: %v", wire)
	}
	a, err := s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, p.ProfileID, wire.EventId, digestEvent(wire))
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision == 0 || a.ConsumptionID == "" || !bytes.Equal(a.Digest, digestEvent(wire)) {
		t.Fatal("missing authority")
	}
	if again := record(event); again == nil || again.EventId != wire.EventId {
		t.Fatal("exact replay changed its host event")
	}
	// The same caller ID with a different payload is a different event, not a
	// conflict; without a source for its state it is rejected on its own.
	bad := proto.Clone(event).(*pluginv1.WatchSyncEvent)
	bad.PositionSeconds = 90
	if record(bad) != nil {
		t.Fatal("event without a matching source accepted")
	}
	if _, err = s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, "foreign", wire.EventId, digestEvent(wire)); !errors.Is(err, ErrAuthority) {
		t.Fatal("foreign profile resolved")
	}
	if _, err = s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, p.ProfileID, wire.EventId, make([]byte, 32)); !errors.Is(err, ErrAuthority) {
		t.Fatal("wrong digest resolved")
	}
	if _, err = s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, p.ProfileID, event.EventId, digestEvent(event)); !errors.Is(err, ErrAuthority) {
		t.Fatal("caller event ID resolved without a host receipt")
	}
	history := proto.Clone(event).(*pluginv1.WatchSyncEvent)
	history.EventId = "viewing"
	history.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED
	history.PlaybackSessionId = ""
	history.Completed = false
	history.PositionSeconds = 0
	historyWire := record(history)
	if historyWire == nil {
		t.Fatal("history export rejected")
	}
	if !historyWire.OccurredAt.AsTime().Equal(wire.OccurredAt.AsTime()) {
		t.Fatal("deliveries of one viewing carry different occurrence times")
	}
	h, err := s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, p.ProfileID, historyWire.EventId, digestEvent(historyWire))
	if err != nil {
		t.Fatal(err)
	}
	if h.ConsumptionID != a.ConsumptionID || h.Revision >= a.Revision {
		t.Fatal("history/live overlap lost source ordering or stable consumption")
	}
	if err = s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveEvent(ctx, id, info.InstanceID, p.AccountID, p.ProfileID, wire.EventId, digestEvent(wire)); !errors.Is(err, ErrAuthority) {
		t.Fatal("revoked event authority survives")
	}
}
