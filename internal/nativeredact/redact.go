// Package nativeredact keeps Bloem's private tool input arguments out of
// Silo's logs. A native input (a storage-source file or a `.strm` item) is
// handed to FFmpeg and ffprobe as a loopback URL whose path is the
// capability that reads the source (nativestorage.ToolInput). Silo passes
// FFmpeg's command line and output through fspath.RedactRelayed before it
// logs them (the start and restart command lines in internal/playback, the
// FFmpeg log sink, the node error bodies), so Register wraps the registered
// fspath relay in one whose Redact also replaces those URLs. It answers for
// nothing else: every other method is the relay registered before it, and a
// tool input argument is never a relayed source.
package nativeredact

import (
	"context"
	"os"
	"regexp"
	"sync"

	"github.com/Silo-Server/silo-server/internal/fspath"
)

// CapabilityLength is the length of a tool input capability: 32 random bytes
// in unpadded base64url.
const CapabilityLength = 43

// toolInputArgument matches a tool input argument: the loopback listener and
// the capability. Silo's core-access relay URL (/src/<32 hex>) never matches.
var toolInputArgument = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]{1,5}/[A-Za-z0-9_-]{43}`)

// Redacted is what a redacted tool input argument reads as.
const Redacted = "native source"

// Redact replaces every tool input argument in s with Redacted.
func Redact(s string) string {
	return toolInputArgument.ReplaceAllLiteralString(s, Redacted)
}

var register sync.Once

// Register makes fspath.RedactRelayed redact tool input arguments in this
// process, on top of the relay already registered. Call it on the API
// servers only, where native inputs run; repeated calls do nothing. A
// transcode node runs no Bloem code and keeps Silo's relay alone.
func Register() {
	register.Do(func() {
		fspath.RegisterRelayedSources(relay{next: fspath.RegisteredRelayedSources()})
	})
}

// relay is the fspath relay Register installs.
type relay struct{ next fspath.RelayedSources }

func (r relay) Owns(name string) bool { return r.next != nil && r.next.Owns(name) }

func (r relay) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if r.next == nil {
		return nil, fspath.ErrRelayed
	}
	return r.next.Stat(ctx, name)
}

func (r relay) OpenReader(ctx context.Context, name string) (fspath.Reader, error) {
	if r.next == nil {
		return nil, fspath.ErrRelayed
	}
	return r.next.OpenReader(ctx, name)
}

func (r relay) GuardArgs(probe bool, args []string) []string {
	if r.next == nil {
		return args
	}
	return r.next.GuardArgs(probe, args)
}

func (r relay) Redact(s string) string {
	if r.next != nil {
		s = r.next.Redact(s)
	}
	return Redact(s)
}
