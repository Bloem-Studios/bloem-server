// Package storagesource persists Bloem native storage discovery. Journal entries
// are staging data, not indexed catalog items or authorization grants.
package storagesource

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrStaleLease         = errors.New("storage scan lease is stale")
	ErrSourceUnavailable  = errors.New("storage source unavailable")
	ErrReferenceConflict  = errors.New("storage catalog reference conflicts")
	ErrCheckpointConflict = errors.New("storage discovery checkpoint conflicts")
	ErrSourceInUse        = errors.New("storage source already backs a library")
)

type SourceConfig struct {
	Key uuid.UUID
	// Key is the retained resource UUID; OwnerID survives installation removal.
	OwnerID               uuid.UUID
	InstallationID        *int64
	PluginID              string
	ProviderSourceID      string
	RootEntryID           string
	ConfigurationRevision int64
	Enabled               bool
}

// Location is a library's storage location: the source its files come from.
type Location struct {
	ID, SourceKey uuid.UUID
	FolderID      int
}
type PersistedRef struct {
	LocationID                     uuid.UUID
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func validText(s string, max int, required bool) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
