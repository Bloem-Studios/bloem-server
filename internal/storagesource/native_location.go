package storagesource

import (
	"path"
	"strings"
)

// IsNativeLocation reports whether a raw location's bytes come only from a
// Bloem reader, never from serving or opening the OS path: the reserved native
// identity namespace (even malformed identities must never become OS paths),
// and a local `.strm` file, whose text is a remote URL that must never reach a
// client. Physical paths containing the prefix only in a basename remain
// ordinary paths.
func IsNativeLocation(location string) bool {
	return strings.HasPrefix(location, "bloem-storage:") || strings.EqualFold(path.Ext(location), ".strm")
}
