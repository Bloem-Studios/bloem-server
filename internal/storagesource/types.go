// Package storagesource persists Bloem native storage discovery. Journal entries
// are staging data, not indexed catalog items or authorization grants.
package storagesource

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrStaleLease         = errors.New("storage scan lease is stale")
	ErrSourceUnavailable  = errors.New("storage source unavailable")
	ErrReferenceConflict  = errors.New("storage catalog reference conflicts")
	ErrCheckpointConflict = errors.New("storage discovery checkpoint conflicts")
)

type SourceConfig struct {
	Key                   uuid.UUID
	InstallationID        *int64
	PluginID              string
	ProviderSourceID      string
	RootEntryID           string
	ConfigurationRevision int64
	Enabled               bool
}

type Binding struct {
	ID, SourceKey uuid.UUID
	FolderID      int
}
type PersistedRef struct {
	BindingID                      uuid.UUID
	EntryID, Revision, LogicalPath string
}
type Lease struct {
	RunID, SourceKey             uuid.UUID
	ConfigurationRevision, Epoch int64
	Owner                        string
}
type Checkpoint struct {
	DirectoryID, Cursor string
	Complete            bool
}
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func validText(s string, max int, required bool) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
