package handlers

import (
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// bloemStrmSidecar reports whether a native file's external subtitle at
// index is a subtitle file beside a local `.strm` item. The subtitle route
// then serves it as Silo serves any local sidecar, after the native source
// preflight it runs for every native subtitle. Every other native external
// subtitle stays refused.
func bloemStrmSidecar(file *models.MediaFile, index int) bool {
	if file == nil || index < 0 || index >= len(file.ExternalSubtitles) {
		return false
	}
	return scanner.StrmSidecarSubtitle(file.FilePath, file.ExternalSubtitles[index].Path)
}
