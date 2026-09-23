package artworkkey

import (
	"path"
	"strings"
)

// ImageTypeFromPath returns the image type segment ("poster", "backdrop",
// "logo", "still", "profile") from a cached key shaped like
// ".../{imageType}/{variant}.{ext}". Empty for URLs or paths without a
// directory segment.
func ImageTypeFromPath(objectPath string) string {
	objectPath = strings.TrimSpace(objectPath)
	if objectPath == "" || strings.Contains(objectPath, "://") {
		return ""
	}
	dir := path.Dir(objectPath)
	if dir == "." || dir == "/" {
		return ""
	}
	return path.Base(dir)
}
