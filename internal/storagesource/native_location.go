package storagesource

import "strings"

// IsNativeLocation reports whether a raw location is in the reserved native
// identity namespace. Even malformed identities must never become OS paths.
// Physical paths containing the prefix only in a basename remain ordinary paths.
func IsNativeLocation(location string) bool {
	return strings.HasPrefix(location, "bloem-storage:")
}
