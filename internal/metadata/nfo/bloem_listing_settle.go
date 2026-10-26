package nfo

import (
	"io/fs"
	"time"
)

// lastChange is the later of a directory's modification and change times.
// Bloem: the NFO listing cache waits for it, not the modification time alone,
// to settle before keeping a listing. A copy tool can set a directory's
// modification time back but not its change time, so a listing read in the
// change time's tick could otherwise miss a later change in that tick.
func lastChange(info fs.FileInfo) time.Time {
	changed := info.ModTime()
	if ct := changeTime(info); ct > changed.UnixNano() {
		changed = time.Unix(0, ct)
	}
	return changed
}
