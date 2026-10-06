package jellycompat

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// GuardNativeStorageFiles hides native identities from compatibility handlers
// that require OS filenames. Native attachment delivery is not implemented.
func GuardNativeStorageFiles(files FilePathResolver) FilePathResolver {
	if files == nil {
		return nil
	}
	return nativeStorageFiles{FilePathResolver: files}
}

type nativeStorageFiles struct{ FilePathResolver }

func (r nativeStorageFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FilePathResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) {
		return nil, scanner.ErrFileNotFound
	}
	return file, err
}
