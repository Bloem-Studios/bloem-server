package remotesource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AllowPrivateSettingKey is the platform setting that lets `.strm` URLs reach
// addresses on the server's private network. Off by default.
const AllowPrivateSettingKey = "bloem.strm.allow_private_addresses"

// Source is the remote media behind one catalog file.
type Source struct {
	// FilePath is media_files.file_path of the `.strm` item: a local path or
	// a native storage catalog location.
	FilePath string
	URL      string
	// Revision is Revision(URL).
	Revision string
	// SourceRevision is the storage entry revision of a `.strm` object in a
	// storage source, empty for a local file.
	SourceRevision string
	RemoteSize     int64
}

// DB is the part of pgx the store uses.
type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store keeps bloem_remote_sources.
type Store struct{ db DB }

// NewStore wraps a pool or transaction.
func NewStore(db DB) *Store { return &Store{db: db} }

// Get returns the source for a catalog path; ok is false when there is none.
func (s *Store) Get(ctx context.Context, filePath string) (Source, bool, error) {
	if s == nil || s.db == nil {
		return Source{}, false, nil
	}
	out := Source{FilePath: filePath}
	err := s.db.QueryRow(ctx, `SELECT url, revision, source_revision, remote_size FROM bloem_remote_sources WHERE file_path=$1`, filePath).
		Scan(&out.URL, &out.Revision, &out.SourceRevision, &out.RemoteSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, false, nil
	}
	if err != nil {
		return Source{}, false, err
	}
	return out, true, nil
}

// Put records the source for a path, replacing an older URL.
func (s *Store) Put(ctx context.Context, src Source) error {
	if s == nil || s.db == nil || src.FilePath == "" || ValidateURL(src.URL) != nil || src.RemoteSize < 0 {
		return ErrInvalidStrm
	}
	src.Revision = Revision(src.URL)
	_, err := s.db.Exec(ctx, `INSERT INTO bloem_remote_sources(file_path,url,revision,source_revision,remote_size,updated_at)
		VALUES($1,$2,$3,$4,$5,NOW())
		ON CONFLICT(file_path) DO UPDATE SET url=EXCLUDED.url,revision=EXCLUDED.revision,
		  source_revision=EXCLUDED.source_revision,remote_size=EXCLUDED.remote_size,updated_at=NOW()`,
		src.FilePath, src.URL, src.Revision, src.SourceRevision, src.RemoteSize)
	return err
}

// Delete forgets a path's source, so it stops playing.
func (s *Store) Delete(ctx context.Context, filePath string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `DELETE FROM bloem_remote_sources WHERE file_path=$1`, filePath)
	return err
}

// AllowPrivate reads the platform setting. A read failure means refused.
func (s *Store) AllowPrivate(ctx context.Context) bool {
	if s == nil || s.db == nil {
		return false
	}
	var value string
	if err := s.db.QueryRow(ctx, `SELECT value FROM server_settings WHERE key=$1`, AllowPrivateSettingKey).Scan(&value); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

// cachedSetting re-reads a setting at most once per ttl.
func cachedSetting(ttl time.Duration, read func(context.Context) bool) func(context.Context) bool {
	var (
		mu     sync.Mutex
		value  bool
		readAt time.Time
	)
	return func(ctx context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		if !readAt.IsZero() && time.Since(readAt) < ttl {
			return value
		}
		// The setting read must not inherit a request's cancellation, or a
		// cancelled request would cache "refused" for everyone.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		value, readAt = read(readCtx), time.Now()
		cancel()
		return value
	}
}

// Runtime is one process's store and guard for a database: every scanner,
// ingest consumer and playback path shares its connection pool and setting
// cache.
type Runtime struct {
	Store *Store
	Guard *Guard
}

var runtimes sync.Map // *pgxpool.Pool -> *Runtime

// ForPool returns the shared runtime for a pool, or nil without one.
func ForPool(pool *pgxpool.Pool) *Runtime {
	if pool == nil {
		return nil
	}
	if existing, ok := runtimes.Load(pool); ok {
		if rt, ok := existing.(*Runtime); ok {
			return rt
		}
	}
	store := NewStore(pool)
	created := &Runtime{Store: store, Guard: NewGuard(Options{AllowPrivate: cachedSetting(5*time.Second, store.AllowPrivate)})}
	actual, _ := runtimes.LoadOrStore(pool, created)
	if rt, ok := actual.(*Runtime); ok {
		return rt
	}
	return created
}
