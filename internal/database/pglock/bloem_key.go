package pglock

import "hash/fnv"

// Key derives a stable advisory-lock key from a name. Bloem-owned: upstream's
// pglock takes a caller-supplied int64 and has no naming scheme of its own.
// Copied unchanged from the retired internal/dblock so lock identities are
// preserved across the switch — a different hash here would silently stop two
// nodes from contending on the same lock.
func Key(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}
