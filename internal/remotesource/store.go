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
	// Validators and Probe are what the last probe saw and found: a rescan
	// that sees the same URL with unchanged validators reuses Probe (JSON of
	// the scanner's probe) instead of reading the media. Probe is nil when
	// no probe was recorded. Get fills Validators.Size from RemoteSize.
	Validators Validators
	Probe      []byte
}

// DB is the part of pgx the store uses.
type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
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
	out, err := scanSource(s.db.QueryRow(ctx, `SELECT `+sourceColumns+` FROM bloem_remote_sources WHERE file_path=$1`, filePath))
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, false, nil
	}
	if err != nil {
		return Source{}, false, err
	}
	return out, true, nil
}

const sourceColumns = `file_path, url, revision, source_revision, remote_size, etag, last_modified, probe`

func scanSource(row pgx.Row) (Source, error) {
	var (
		out      Source
		modified *time.Time
	)
	if err := row.Scan(&out.FilePath, &out.URL, &out.Revision, &out.SourceRevision, &out.RemoteSize, &out.Validators.ETag, &modified, &out.Probe); err != nil {
		return Source{}, err
	}
	out.Validators.Size = out.RemoteSize
	if modified != nil {
		out.Validators.LastModified = modified.UTC()
	}
	return out, nil
}

// GetMany returns the sources of a page of catalog paths, keyed by path, in
// one query. Paths without a source are absent.
func (s *Store) GetMany(ctx context.Context, filePaths []string) (map[string]Source, error) {
	out := make(map[string]Source, len(filePaths))
	if s == nil || s.db == nil || len(filePaths) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+sourceColumns+` FROM bloem_remote_sources WHERE file_path = ANY($1)`, filePaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out[src.FilePath] = src
	}
	return out, rows.Err()
}

// Put records the source for a path, replacing an older URL.
func (s *Store) Put(ctx context.Context, src Source) error {
	if s == nil || s.db == nil || src.FilePath == "" || ValidateURL(src.URL) != nil || src.RemoteSize < 0 {
		return ErrInvalidStrm
	}
	src.Revision = Revision(src.URL)
	var modified *time.Time
	if !src.Validators.LastModified.IsZero() {
		at := src.Validators.LastModified.UTC()
		modified = &at
	}
	var probe any
	if len(src.Probe) > 0 {
		probe = string(src.Probe)
	}
	_, err := s.db.Exec(ctx, `INSERT INTO bloem_remote_sources(file_path,url,revision,source_revision,remote_size,etag,last_modified,probe,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,NOW())
		ON CONFLICT(file_path) DO UPDATE SET url=EXCLUDED.url,revision=EXCLUDED.revision,
		  source_revision=EXCLUDED.source_revision,remote_size=EXCLUDED.remote_size,
		  etag=EXCLUDED.etag,last_modified=EXCLUDED.last_modified,probe=EXCLUDED.probe,updated_at=NOW()`,
		src.FilePath, src.URL, src.Revision, src.SourceRevision, src.RemoteSize, src.Validators.ETag, modified, probe)
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
