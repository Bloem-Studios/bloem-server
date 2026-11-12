package livetv

import (
	"encoding/json"
	"strings"
	"testing"
)

// A recording's file path on the server is never sent to a client: Silo
// shows file paths only to viewers who curate metadata (Silo #3999), and any
// viewer with Live TV can list their own recordings. The store and recorder
// keep the path in its own column.
func TestRecordingJSONOmitsServerPath(t *testing.T) {
	wire, err := json.Marshal(Recording{ID: "rec-1", Path: "/recordings/News/rec-1.ts", Title: "News"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "rec-1.ts") || strings.Contains(string(wire), `"path"`) {
		t.Fatalf("recording JSON carries the server path: %s", wire)
	}
}
