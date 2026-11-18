package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
)

type helloRecordingObserver struct {
	RemoteObserver
	commands []playback.CommandName
}

func (o *helloRecordingObserver) OnHello(_ context.Context, _ remote.SessionInfo, commands []playback.CommandName) {
	o.commands = append([]playback.CommandName(nil), commands...)
}

// Silo's hello validation runs over Silo's command names, so a hello that
// also lists Bloem's remote-control commands is accepted, and Bloem's
// observer still receives the full list.
func TestRealtimeHelloAcceptsRemoteControlCommands(t *testing.T) {
	file := &models.MediaFile{ID: 42, ContentID: "movie-1", Duration: 3600}
	sessionMgr := playback.NewSessionManager(0, 0)
	handler := newKeyedPlaybackHandler(sessionMgr, testPlaybackFileResolver{file: file})
	observer := &helloRecordingObserver{}
	handler.RemoteObserver = observer
	session, err := sessionMgr.StartSession(1, "profile-1", file.ID, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	commands := []playback.CommandName{playback.CommandPause, remote.CommandReplan}
	data, err := json.Marshal(playback.HelloEnvelope{
		Type:         playback.RealtimeMessageTypeHello,
		SessionID:    session.ID,
		Client:       playback.HelloClientInfo{Name: "web", Version: "1"},
		Capabilities: playback.HelloCapabilities{Commands: commands},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.handleRealtimeClientMessage(session.ID, data); err != nil {
		t.Fatalf("hello with a remote-control command refused: %v", err)
	}
	if len(observer.commands) != 2 || observer.commands[1] != remote.CommandReplan {
		t.Fatalf("observed commands = %v, want %v", observer.commands, commands)
	}

	unknown, err := json.Marshal(playback.HelloEnvelope{
		Type:         playback.RealtimeMessageTypeHello,
		SessionID:    session.ID,
		Client:       playback.HelloClientInfo{Name: "web", Version: "1"},
		Capabilities: playback.HelloCapabilities{Commands: []playback.CommandName{"reboot"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.handleRealtimeClientMessage(session.ID, unknown); err == nil {
		t.Fatal("hello with a command neither Silo nor Bloem knows was accepted")
	}
}
