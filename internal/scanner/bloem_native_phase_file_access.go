package scanner

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// NativePhaseFileLookup reuses the original file inventory/decoder with the
// producer's supplied query, so moved files and newly inserted rows are visible
// to the existing MediaFileAuthorizer inside that same phase.
type NativePhaseFileLookup struct{ Query catalog.NativePhaseQuery }

func (a NativePhaseFileLookup) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	if a.Query == nil {
		return nil, fmt.Errorf("native phase query unavailable")
	}
	return scanMediaFile(a.Query.QueryRow(ctx, `SELECT `+fileColumns+` FROM media_files WHERE id=$1`, id))
}
