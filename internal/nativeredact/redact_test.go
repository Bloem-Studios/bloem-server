package nativeredact

import (
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/fspath"
	_ "github.com/Silo-Server/silo-server/internal/nodesource" // registers Silo's core-access relay, as in every server process
)

// The redaction relay wraps Silo's: a tool input argument reads as
// "native source", a core-access relay URL is still Silo's to redact and to
// answer for, and a tool input is no relayed source.
func TestRedactionWrapsTheCoreAccessRelay(t *testing.T) {
	Register()
	Register() // a second call wraps nothing more
	tool := "http://127.0.0.1:45815/" + strings.Repeat("A", CapabilityLength-1) + "g"
	relay := "http://127.0.0.1:41000/src/" + strings.Repeat("0a", 16)
	got := fspath.RedactRelayed("ffmpeg -i " + tool + " -i " + relay)
	if got != "ffmpeg -i native source -i source (released)" {
		t.Fatalf("redacted = %q", got)
	}
	if !fspath.IsRelayed(relay) || fspath.IsRelayed(tool) || fspath.IsRelayed("bloem-storage:v1:abc") {
		t.Fatal("redaction changed which inputs are relayed")
	}
	if args := fspath.CommandContext(t.Context(), "ffmpeg", "-i", tool).Args; strings.Join(args[1:], " ") != "-i "+tool {
		t.Fatalf("a tool input launch was guarded as relayed: %q", args)
	}
}
