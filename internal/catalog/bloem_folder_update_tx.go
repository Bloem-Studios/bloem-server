package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// UpdateTx applies Update's field and path changes inside a caller-owned
// transaction, so a caller can commit them together with its own rows (a
// native library's location replacement writes its source selections in the
// same transaction). It sets the same columns as Update, through Silo's
// folderUpdateAssignments, and replaces paths the same way; it does not
// report whether enabled changed.
func (r *FolderRepository) UpdateTx(ctx context.Context, tx pgx.Tx, id int, input UpdateFolderInput) error {
	if err := updateFolderRowTx(ctx, tx, id, input); err != nil {
		return err
	}
	if input.Paths == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, "DELETE FROM media_folder_paths WHERE media_folder_id = $1", id); err != nil {
		return fmt.Errorf("deleting old paths: %w", err)
	}
	for _, p := range *input.Paths {
		if _, err := tx.Exec(ctx, `INSERT INTO media_folder_paths (media_folder_id, path) VALUES ($1, $2)`, id, p); err != nil {
			if isDuplicateKeyError(err) {
				return fmt.Errorf("%w: %s", ErrDuplicatePath, extractConstraint(err))
			}
			return fmt.Errorf("inserting folder path %q: %w", p, err)
		}
	}
	return nil
}

// updateFolderRowTx sets the folder's changed columns, or only checks that it
// exists when nothing but its paths changes.
func updateFolderRowTx(ctx context.Context, tx pgx.Tx, id int, input UpdateFolderInput) error {
	setClauses, args := folderUpdateAssignments(input)
	if len(setClauses) == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_folders WHERE id = $1)", id).Scan(&exists); err != nil {
			return fmt.Errorf("checking folder existence: %w", err)
		}
		if !exists {
			return ErrFolderNotFound
		}
		return nil
	}
	query := fmt.Sprintf("UPDATE media_folders SET %s WHERE id = $%d", strings.Join(setClauses, ", "), len(args)+1)
	tag, err := tx.Exec(ctx, query, append(args, id)...)
	if err != nil {
		return fmt.Errorf("updating folder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrFolderNotFound
	}
	return nil
}
