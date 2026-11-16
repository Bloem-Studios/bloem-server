package nativestorage

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativeredact"
)

// The argument a tool input hands FFmpeg is what nativeredact redacts in
// Silo's command logs: a change to its form must change the redaction too.
func TestToolInputArgumentIsRedacted(t *testing.T) {
	file, err := mediasource.Open(t.Context(), &toolFixtureSource{data: "0123456789abcdef"}, mediasource.Ref{SourceID: "source", EntryID: "entry", Revision: "immutable-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewToolInput(t.Context(), file, ToolInputConfig{
		CancelSource: func() {}, Reauthorize: func(context.Context) error { return nil },
		MaxBytes: 64, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var argument string
	if err := input.Run(func(_ context.Context, arg string) error { argument = arg; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := nativeredact.Redact("-i " + argument + " -c copy"); got != "-i "+nativeredact.Redacted+" -c copy" {
		t.Fatalf("argument %q redacts to %q", argument, got)
	}
}
