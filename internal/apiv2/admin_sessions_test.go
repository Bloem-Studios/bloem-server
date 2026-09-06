package apiv2

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type fakeAdminPlaybackSessions struct{ calls int }

func (*fakeAdminPlaybackSessions) AdminPlaybackSessionsAvailable() bool { return true }
func (f *fakeAdminPlaybackSessions) ReadAdminPlaybackSessions(context.Context) ([]handlers.AdminPlaybackSessionView, error) {
	f.calls++
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 3600))
	return []handlers.AdminPlaybackSessionView{
		{SessionID: "b", UserID: 7, ProfileID: "child", MediaFileID: 42, RequestedMediaFileID: 41, StartedAt: at, UpdatedAt: at, RoutingExecutionNodeID: new(9), TargetAudioChannels: new(2), SourceAudioChannels: new(8), EffectivePlayMethod: "transcode", IsJellyfinClient: true, HasPlaybackControl: true},
		{SessionID: "a", UserID: 7, ProfileID: "primary", MediaFileID: 44, RequestedMediaFileID: 44, StartedAt: at, UpdatedAt: at},
	}, nil
}

type fakeAdminNodeSessions struct{ calls, node int }

func (*fakeAdminNodeSessions) Available() bool { return true }
func (f *fakeAdminNodeSessions) Read(_ context.Context, node int) (nodesessions.ListResult, error) {
	f.calls++
	f.node = node
	return nodesessions.ListResult{Undecodable: 1, Sessions: []nodesessions.SessionInfo{
		{NodeURL: "https://node.invalid", SessionID: "same", AuthUserID: 7, MediaFileID: 42, Executor: &playback.ExecutorNamespaceV3{Incarnation: "inc", Epoch: 2, ExecutorID: "new"}},
		{NodeURL: "https://node.invalid", SessionID: "same", AuthUserID: 7, MediaFileID: 42, Executor: &playback.ExecutorNamespaceV3{Incarnation: "inc", Epoch: 1, ExecutorID: "old"}},
	}}, nil
}
func TestAdminPlaybackSessionReadProjection(t *testing.T) {
	deps := pilotDeps(nil, nil)
	f := new(fakeAdminPlaybackSessions)
	deps.AdminPlaybackSessions = f
	h := NewHandler(deps)
	path := Prefix + "/admin/sessions"
	requireProblem(t, do(t, h, "GET", path, "", bearer(memberToken)), TypePermissionDenied)
	if f.calls != 0 {
		t.Fatal("member reached loader")
	}
	rec := do(t, h, "GET", path+"?limit=1", "", bearer(adminToken))
	var page Collection[AdminPlaybackSession]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(page.Items) != 1 || page.Items[0].SessionID != "a" || page.Items[0].UserID != "7" || page.Items[0].ProfileID != "primary" || page.Page == nil || !page.Page.HasMore {
		t.Fatal(rec.Code, rec.Body.String())
	}
	cursor := url.QueryEscape(page.Page.NextCursor)
	rec = do(t, h, "GET", path+"?limit=1&cursor="+cursor, "", bearer(adminToken))
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	row := page.Items[0]
	if rec.Code != 200 || row.SessionID != "b" || row.ProfileID != "child" || row.MediaFileID != "42" || row.RequestedMediaFileID != "41" || row.RoutingExecutionNodeID == nil || *row.RoutingExecutionNodeID != "9" || *row.TargetAudioChannels != 2 || *row.SourceAudioChannels != 8 || !row.IsJellyfinClient || !row.HasPlaybackControl || page.Page.HasMore {
		t.Fatal(rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"started_at":"2026-01-02T02:04:05.123Z"`) {
		t.Fatal(rec.Body.String())
	}
	requireProblem(t, do(t, h, "GET", path+"?limit=2&cursor="+cursor, "", bearer(adminToken)), TypeInvalidCursor)
	requireProblem(t, do(t, h, "GET", path+"?limit=1&cursor="+cursor, "", actingRequestAdmin), TypeInvalidCursor)
	missing := NewHandler(pilotDeps(nil, nil))
	requireProblem(t, do(t, missing, "GET", path, "", bearer(adminToken)), TypeDependencyUnavailable)
	rec = do(t, missing, "GET", path+"/capabilities", "", bearer(adminToken))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatal(rec.Body.String())
	}
}
func TestAdminNodeSessionObservations(t *testing.T) {
	deps := pilotDeps(nil, nil)
	f := new(fakeAdminNodeSessions)
	deps.AdminNodeSessions = f
	h := NewHandler(deps)
	path := Prefix + "/admin/node-sessions"
	requireProblem(t, do(t, h, "GET", path, "", bearer(memberToken)), TypePermissionDenied)
	requireProblem(t, do(t, h, "GET", path+"?node_id=0", "", bearer(adminToken)), TypeValidationFailed)
	if f.calls != 0 {
		t.Fatal("invalid read reached source")
	}
	rec := do(t, h, "GET", path+"?limit=1&node_id=9", "", bearer(adminToken))
	var out AdminNodeSessionsOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &out.Body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || f.node != 9 || out.Body.Undecodable != 1 || len(out.Body.Items) != 1 || !out.Body.Page.HasMore || out.Body.Items[0].AuthUserID != "7" || out.Body.Items[0].StartedAt != nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
	first := out.Body.Items[0].Executor.ExecutorID
	cursor := url.QueryEscape(out.Body.Page.NextCursor)
	rec = do(t, h, "GET", path+"?limit=1&node_id=9&cursor="+cursor, "", bearer(adminToken))
	if err := json.Unmarshal(rec.Body.Bytes(), &out.Body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(out.Body.Items) != 1 || out.Body.Page.HasMore || out.Body.Items[0].Executor.ExecutorID == first {
		t.Fatal(rec.Body.String())
	}
	requireProblem(t, do(t, h, "GET", path+"?limit=1&node_id=8&cursor="+cursor, "", bearer(adminToken)), TypeInvalidCursor)
}
func adminSessionFixtureCases() []fixtureCase {
	return []fixtureCase{
		{name: "admin_playback_sessions", operationID: "listAdminPlaybackSessions", method: "GET", path: Prefix + "/admin/sessions", headers: bearer(adminToken), status: 200, schema: "#/components/schemas/CollectionAdminPlaybackSession", assertHeaders: []string{"Content-Type"}, scenario: "Diagnostic rows retain account/profile and chosen/requested file distinctions with canonical string IDs and instants."},
	}
}
