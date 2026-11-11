package handlers

// Bloem playback v3 handler coverage moved out of Silo's playback_v3_test.go so
// that file carries only the unavoidable Bloem seams.

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestTerminalAllowsAlternateFileV3IncludesSubtitleConversionIncompatibility(t *testing.T) {
	if !terminalAllowsAlternateFileV3(&playback.TerminalV3{Reason: "subtitle_conversion_unsupported"}) {
		t.Fatal(`terminal reason "subtitle_conversion_unsupported" should permit alternate selection`)
	}
}
