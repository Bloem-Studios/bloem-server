package userdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// OpenPlaybackSink never initializes, migrates, or changes journal mode on a source.
// Each handle owns its pool independently of the legacy user database cache.
func (p *SQLiteProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Backend != userstore.PlaybackSourceSQLite {
		return nil, userstore.ErrPlaybackSourceMismatch
	}
	if p == nil || p.pool == nil {
		return nil, userstore.ErrPlaybackSourceUnavailable
	}
	path := filepath.Join(p.pool.config.DataDir, fmt.Sprintf("%d.db", ref.AccountID))
	uri := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"rw"}, "_busy_timeout": {"5000"}, "_synchronous": {"FULL"}, "_foreign_keys": {"ON"}}
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, err
	}
	s := &SQLiteUserStore{db: db, sourceRef: &ref}
	conn, err := db.Conn(ctx)
	if err == nil {
		err = s.checkPlaybackSource(ctx, settingMutationConnExecutor{ctx: ctx, conn: conn})
		if err == nil {
			err = verifyPlaybackSourceDurability(ctx, settingMutationConnExecutor{ctx: ctx, conn: conn})
		}
		_ = conn.Close()
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open exact playback source: %w: %w", userstore.ErrPlaybackSourceUnavailable, err)
	}
	return &sqlitePlaybackSinkHandle{SQLiteUserStore: s, ref: ref}, nil
}

type sqlitePlaybackSinkHandle struct {
	*SQLiteUserStore
	ref userstore.PlaybackSourceRef
}

func (s *sqlitePlaybackSinkHandle) Source() userstore.PlaybackSourceRef { return s.ref }
func (s *sqlitePlaybackSinkHandle) Close() error {
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()
	if s.sourceClosed {
		return nil
	}
	s.sourceClosed = true
	return s.db.Close()
}

func (s *SQLiteUserStore) checkPlaybackSource(ctx context.Context, exec preferenceSettingsExecutor) error {
	if s.sourceRef == nil {
		var count int
		if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM playback_source_markers").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return userstore.ErrPlaybackSourceUnbound
		}
		return nil
	}
	var markerCount int
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM playback_source_markers").Scan(&markerCount); err != nil {
		return fmt.Errorf("playback source marker unavailable: %w: %w", userstore.ErrPlaybackSourceUnavailable, err)
	}
	if markerCount != 1 {
		return fmt.Errorf("account database requires exactly one source marker: %w", userstore.ErrPlaybackSourceUnavailable)
	}
	var account int
	var source, gate string
	var generation int64
	err := exec.QueryRowContext(ctx, `SELECT user_id,source_id,selection_generation,gate FROM playback_source_markers WHERE user_id=?`, s.sourceRef.AccountID).Scan(&account, &source, &generation, &gate)
	if err != nil {
		return fmt.Errorf("playback source marker unavailable: %w: %w", userstore.ErrPlaybackSourceUnavailable, err)
	}
	if account != s.sourceRef.AccountID || source != s.sourceRef.SourceID || generation != s.sourceRef.SelectionGeneration {
		return userstore.ErrPlaybackSourceMismatch
	}
	if gate != "writable" {
		return userstore.ErrPlaybackSourceUnavailable
	}
	return nil
}

func verifyPlaybackSourceDurability(ctx context.Context, exec preferenceSettingsExecutor) error {
	var journal string
	var synchronous int
	if err := exec.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("verify playback durability: %w: %w", userstore.ErrPlaybackSourceDurability, err)
	}
	if err := exec.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return fmt.Errorf("verify playback durability: %w: %w", userstore.ErrPlaybackSourceDurability, err)
	}
	if !strings.EqualFold(journal, "wal") || synchronous != 2 {
		return fmt.Errorf("playback source requires WAL and synchronous FULL: %w", userstore.ErrPlaybackSourceDurability)
	}
	return nil
}

var _ userstore.PlaybackSourceProvider = (*SQLiteProvider)(nil)
var _ userstore.PlaybackSinkHandle = (*sqlitePlaybackSinkHandle)(nil)
