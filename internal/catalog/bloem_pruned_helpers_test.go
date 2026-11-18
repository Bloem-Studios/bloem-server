package catalog

import "github.com/Silo-Server/silo-server/internal/models"

// Test helpers Bloem's tests use, copied from Silo's tests before Silo pruned them
// (Silo #4465). Silo's test files no longer carry them.

func h264File(id int, multiplePPS *bool) *models.MediaFile {
	return &models.MediaFile{
		ID:         id,
		CodecVideo: "h264",
		VideoTracks: []models.VideoTrack{{
			Codec:       "h264",
			MultiplePPS: multiplePPS,
		}},
	}
}
