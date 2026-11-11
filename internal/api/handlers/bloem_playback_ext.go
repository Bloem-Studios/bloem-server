package handlers

import (
	"time"
)

// bloemPlaybackHandlerExt holds Bloem-only PlaybackHandler fields; they are
// promoted, so callers keep writing h.RemoteObserver etc.
type bloemPlaybackHandlerExt struct {
	// RemoteObserver mirrors the session socket's hello/ack/result frames
	// into the remote control audit (S-5a). Nil when remote control is not
	// wired; every call site checks.
	RemoteObserver RemoteObserver
	// RemoteCommandDeadline bounds how long a remote command may wait for an
	// ack before the server-side fallback fires. Zero means the copy-safety
	// invalidation deadline; tests shorten it.
	RemoteCommandDeadline time.Duration
}
