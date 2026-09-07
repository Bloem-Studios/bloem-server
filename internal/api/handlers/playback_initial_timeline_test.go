package handlers

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"os"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type initialTimelineResolverFunc func(context.Context, int, string, int) (playback.ClientPlaybackManifestV3, error)

func (f initialTimelineResolverFunc) ResolveClientPlaybackManifest(ctx context.Context, user int, profile string, file int) (playback.ClientPlaybackManifestV3, error) {
	return f(ctx, user, profile, file)
}

func configureInitialTimelineFixture(t *testing.T, f *initialHTTPFixture) (context.Context, PlaybackCaller, playback.ClientPlaybackManifestV3) {
	t.Helper()
	ctx := apimw.SetClaims(t.Context(), &auth.Claims{UserID: f.userID, Role: "user", TokenType: auth.TokenTypeAccess})
	ctx = apimw.SetProfileID(ctx, f.request.ProfileID)
	ctx = access.SetScope(ctx, access.Scope{UserID: f.userID, ProfileID: f.request.ProfileID, ProfileVerified: true})
	var folder int
	if err := f.pool.QueryRow(ctx, `SELECT media_folder_id FROM media_files WHERE id=$1`, f.file.ID).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,content_rating,sort_title,default_metadata_language,original_title,year,runtime,overview,tagline,imdb_id,tmdb_id,tvdb_id,original_language,show_status) VALUES($1,'audiobook','Fixture','PG','fixture','en','Fixture',2026,1,'','','','','','en','')`, f.itemID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, f.itemID)
	})
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, f.itemID, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE media_files SET content_id=$2,duration=400,probe_source='local',presentation_kind='multipart',presentation_group_key='synthetic',presentation_part_index=2,presentation_part_total=2 WHERE id=$1`, f.file.ID, f.itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,duration,probe_source,presentation_kind,presentation_group_key,presentation_part_index,presentation_part_total) VALUES($1,$2,$3,600,'local','multipart','synthetic',1,2)`, f.itemID, folder, f.file.FilePath+".part1"); err != nil {
		t.Fatal(err)
	}
	f.flow.InstallationID = uuid.NewString()
	f.flow.TimelineResolver = catalog.NewClientPlaybackManifestResolver(f.pool)
	manifest, err := f.flow.TimelineResolver.ResolveClientPlaybackManifest(ctx, f.userID, f.request.ProfileID, f.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.file.Duration = 400
	f.request.ProgressPersistence = playback.ProgressPersistenceClientBoundV3
	f.request.TimelineID = manifest.TimelineID
	f.request.ClientFeatures = append(f.request.ClientFeatures, playback.FeatureBoundClientTimelineV3)
	f.request.StartPosition = new(30.0)
	return ctx, PlaybackCaller{UserID: f.userID, ProfileID: f.request.ProfileID, InstallationID: f.flow.InstallationID}, manifest
}

func TestInitialTimelineRuntimeKeepsLocalClockAndTerminalBarrier(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx, caller, manifest := configureInitialTimelineFixture(t, f)
	discovered, err := f.handler.GetClientPlaybackTimeline(ctx, caller, f.file.ID)
	if err != nil || discovered.TimelineID != manifest.TimelineID {
		t.Fatalf("discovery: %+v %v", discovered, err)
	}
	decision, err := f.handler.StartInitialPlayback(ctx, caller, f.request)
	if err != nil || decision.ProgressTimeline == nil {
		t.Fatalf("start: %+v %v", decision, err)
	}
	if decision.ProgressTimeline.PartOffsetSeconds != 600 {
		t.Fatal("wrong selected part")
	}
	// A cross-part seek is a new explicit start for the first catalog part.
	data, readErr := os.ReadFile(f.file.FilePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	f.file.ID, f.file.Duration = manifest.Parts[0].FileID, 600
	f.file.FilePath += ".part1"
	if err := os.WriteFile(f.file.FilePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	second := f.request
	second.FileID = f.file.ID
	second.StartPosition = new(10.0)
	second.PlaybackAttemptID = uuid.NewString()
	if _, err := f.handler.StartInitialPlayback(ctx, caller, second); err == nil {
		t.Fatal("concurrent timeline part admitted")
	} else if op, ok := errors.AsType[*PlaybackOperationError](err); !ok || op.Status != 409 || op.Code != "timeline_part_active" {
		t.Fatalf("wrong part refusal: %v", err)
	}
	progress, err := f.handler.ApplyInitialProgress(ctx, caller, decision.SessionID, PlaybackProgressCommand{TimelineID: manifest.TimelineID, Sequence: 1, Position: 30})
	if err != nil || progress.Accepted == nil || progress.Accepted.Position != 30 || progress.Accepted.ItemPosition == nil || *progress.Accepted.ItemPosition != 630 {
		t.Fatalf("progress: %+v %v", progress, err)
	}
	session, err := f.manager.GetSession(decision.SessionID)
	if err != nil || session.Position != 30 {
		t.Fatalf("runtime clock: %+v %v", session, err)
	}
	if _, err := f.handler.StopInitialPlayback(ctx, caller, decision.SessionID, PlaybackStopCommand{StopID: uuid.NewString(), Sequence: 2, Position: new(35.0)}); err == nil {
		t.Fatal("missing timeline stopped bound session")
	}
	command := PlaybackStopCommand{TimelineID: manifest.TimelineID, StopID: uuid.NewString(), Sequence: 2, Position: new(35.0)}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stopped, err := f.handler.StopInitialPlayback(ctx, caller, decision.SessionID, command)
		if err != nil {
			t.Fatal(err)
		}
		if !stopped.Draining {
			if stopped.Accepted == nil || stopped.Accepted.Position != 35 || stopped.Accepted.ItemPosition == nil || *stopped.Accepted.ItemPosition != 635 {
				t.Fatalf("stop clock: %+v", stopped)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not terminalize")
		}
	}
	second.PlaybackAttemptID = uuid.NewString()
	next, err := f.handler.StartInitialPlayback(ctx, caller, second)
	if err != nil || next.ProgressTimeline == nil || next.ProgressTimeline.FileID != second.FileID || next.ProgressTimeline.PartOffsetSeconds != 0 {
		t.Fatalf("terminal predecessor blocked cross-part intent: %+v %v", next, err)
	}
}

type initialLostTerminalPublication struct {
	InitialPlaybackControlV3
	lost bool
}

func (c *initialLostTerminalPublication) PublishAttempt(ctx context.Context, authority playback.AttemptAuthorityV3, record playback.AttemptRecordV3) error {
	if err := c.InitialPlaybackControlV3.PublishAttempt(ctx, authority, record); err != nil {
		return err
	}
	if c.lost {
		return errors.New("synthetic lost terminal publication response")
	}
	return nil
}
func TestInitialTimelineChangedDecisionIsRetainedBeforeEffects(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "lost response"}[lost], func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			ctx, caller, manifest := configureInitialTimelineFixture(t, f)
			old, err := playback.NewClientPlaybackManifestV3(f.itemID, "old edition", []playback.ClientPlaybackPartV3{{FileID: f.file.ID, DurationSeconds: 1000}})
			if err != nil {
				t.Fatal(err)
			}
			f.request.TimelineID = old.TimelineID
			f.flow.Control = &initialLostTerminalPublication{InitialPlaybackControlV3: f.flow.Control, lost: lost}
			decision, err := f.handler.StartInitialPlayback(ctx, caller, f.request)
			if lost && err == nil {
				t.Fatal("lost publication reported terminal proof")
			}
			if !lost && (err != nil || decision.Terminal == nil || decision.Terminal.Reason != "client_timeline_changed" || decision.Terminal.Retryable) {
				t.Fatalf("terminal decision: %+v %v", decision, err)
			}
			var activationNull, routeNull bool
			var sessions int
			if err := f.pool.QueryRow(ctx, `SELECT control_activation IS NULL,control_route IS NULL,CASE WHEN session_id IS NULL THEN 0 ELSE 1 END FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.request.PlaybackAttemptID).Scan(&activationNull, &routeNull, &sessions); err != nil {
				t.Fatal(err)
			}
			if !activationNull || !routeNull || sessions != 0 {
				t.Fatal("rejected pin produced activation/route/session")
			}
			f.flow.TimelineResolver = initialTimelineResolverFunc(func(context.Context, int, string, int) (playback.ClientPlaybackManifestV3, error) {
				t.Fatal("retained terminal consulted current catalog")
				return manifest, nil
			})
			replay, err := f.handler.StartInitialPlayback(ctx, caller, f.request)
			if err != nil || replay.Terminal == nil || replay.Terminal.Reason != "client_timeline_changed" || replay.SessionID != "" || replay.PlaybackPlan != nil {
				t.Fatalf("terminal replay: %+v %v", replay, err)
			}
		})
	}
}
