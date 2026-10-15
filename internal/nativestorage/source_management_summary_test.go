package nativestorage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
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
