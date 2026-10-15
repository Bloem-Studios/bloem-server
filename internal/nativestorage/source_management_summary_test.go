package nativestorage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
)

func summaryTestView() SourceView {
	id := int64(42)
	return SourceView{InstallationID: &id, PluginID: "bloem.storage.s3", State: "attached", Configured: true, ConnectionSummary: map[string]string{}}
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestWithConnectionSummaryReadFailureLeavesEmptySummaryAndLogsNoDetail(t *testing.T) {
	logs := captureSlog(t)
	s := &SourceManagement{readConnectionConfig: func(context.Context, int) (map[string]any, error) {
		return nil, errors.New("dial tcp: SECRET-ERROR-TEXT access_key=AKIA")
	}}
	got := s.withConnectionSummary(context.Background(), summaryTestView())
	if got.ConnectionSummary == nil || len(got.ConnectionSummary) != 0 {
		t.Fatalf("failed read must leave connection_summary as {}, got %#v", got.ConnectionSummary)
	}
	out := logs.String()
	if !strings.Contains(out, "installation_id=42") || !strings.Contains(out, "native storage connection summary unavailable") {
		t.Fatalf("expected installation id and fixed message, got %q", out)
	}
	for _, leak := range []string{"SECRET-ERROR-TEXT", "AKIA", "access_key"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaked %q: %q", leak, out)
		}
	}
}

func TestWithConnectionSummaryUsesStoredConnection(t *testing.T) {
	logs := captureSlog(t)
	s := &SourceManagement{readConnectionConfig: func(context.Context, int) (map[string]any, error) {
		return map[string]any{"bucket": "media", "access_key": "AKIASECRET"}, nil
	}}
	got := s.withConnectionSummary(context.Background(), summaryTestView())
	if got.ConnectionSummary["bucket"] != "media" || got.ConnectionSummary["access_key"] != "" {
		t.Fatalf("got %v", got.ConnectionSummary)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected logging on success: %q", logs.String())
	}
}

// A platform source an organization is entitled to is readable by that
// organization's administrators, but its connection (bucket, prefix, endpoint,
// region, probe key) belongs to the platform and is not shown across tenancy.
func TestConnectionSummaryForActorScopeAndOwner(t *testing.T) {
	s := &SourceManagement{readConnectionConfig: func(context.Context, int) (map[string]any, error) {
		return map[string]any{"bucket": "media", "prefix": "movies/", "region": "eu-west-1"}, nil
	}}
	for _, tc := range []struct {
		name   string
		scope  auth.AdminScope
		owner  resourcetenancy.OwnerKind
		attach bool
	}{
		{"platform_actor_platform_source", auth.AdminScopePlatform, resourcetenancy.OwnerPlatform, true},
		{"platform_actor_organization_source", auth.AdminScopePlatform, resourcetenancy.OwnerOrganization, true},
		{"organization_actor_platform_source", auth.AdminScopeOrganization, resourcetenancy.OwnerPlatform, false},
		{"organization_actor_organization_source", auth.AdminScopeOrganization, resourcetenancy.OwnerOrganization, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := summaryTestView()
			view.OwnerKind = tc.owner
			got := s.connectionSummaryFor(context.Background(), tc.scope, view)
			if got.ConnectionSummary == nil {
				t.Fatal("connection_summary must never be nil")
			}
			if attached := got.ConnectionSummary["bucket"] == "media"; attached != tc.attach {
				t.Fatalf("attached=%v want %v: %v", attached, tc.attach, got.ConnectionSummary)
			}
			if !tc.attach && len(got.ConnectionSummary) != 0 {
				t.Fatalf("hidden summary must be {}, got %v", got.ConnectionSummary)
			}
		})
	}
}
