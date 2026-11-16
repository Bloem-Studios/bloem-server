package playback

import (
	"context"
	"errors"
	"testing"
)

// A remote start (a transcode start or a download preparation) whose input
// the request's host handles is refused before it is sent, so it runs on the
// API server; every other start is sent as before.
func TestRefuseContextRemoteStartKeepsHostInputsLocal(t *testing.T) {
	native := []byte(`{"session_id":"s","input_path":"bloem-storage:v1:abc"}`)
	if err := RefuseContextRemoteStart(context.Background(), native); err != nil {
		t.Fatalf("no host: %v", err)
	}
	ctx := WithContextMediaHost(context.Background(), &fakeContextMediaHost{handles: "bloem-storage:v1:abc"})
	if err := RefuseContextRemoteStart(ctx, native); !errors.Is(err, ErrContextInputStaysLocal) {
		t.Fatalf("host input: %v", err)
	}
	prepare := []byte(`{"artifact_id":"a","input_path":"bloem-storage:v1:abc"}`)
	if err := RefuseContextRemoteStart(ctx, prepare); !errors.Is(err, ErrContextInputStaysLocal) {
		t.Fatalf("host preparation: %v", err)
	}
	if err := RefuseContextRemoteStart(ctx, []byte(`{"input_path":"/media/Film (2019).mkv"}`)); err != nil {
		t.Fatalf("library file: %v", err)
	}
	if err := RefuseContextRemoteStart(ctx, []byte(`not json`)); !errors.Is(err, ErrContextInputStaysLocal) {
		t.Fatalf("unreadable start: %v", err)
	}
}
