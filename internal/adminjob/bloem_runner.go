package adminjob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalogseed"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/jackc/pgx/v5/pgxpool"
)

// libraryDeletionRepository keeps heartbeat, progress and the final claim-fenced
// result independent of bundle writers occupying the caller's pool. Prepare the
// connection before any destructive work and preserve the original pool hooks.
func (r *Runner) libraryDeletionRepository(ctx context.Context) (*Repository, error) {
	cfg := r.repo.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create library deletion metadata pool: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect library deletion metadata pool: %w", err)
	}
	return &Repository{pool: pool, claim: r.repo.claim}, nil
}

// startLibraryDeletionHeartbeat returns a function that cancels and joins the loop
// before its execution-owned metadata pool closes. No heartbeat remains after
// finalization, and cancellation interrupts a pending database acquisition.
func (r *Runner) startLibraryDeletionHeartbeat(ctx context.Context, jobID string) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		r.heartbeatLoop(heartbeatCtx, jobID, stop)
	}()
	return func() {
		close(stop)
		cancel()
		<-done
	}
}

const (
	libraryDeletionProgressTimeout = time.Second
	libraryDeletionProgressRetry   = 30 * time.Second
)

// libraryDeletionProgress stays synchronous and bounds both the database write
// and event publication. Database contention or delayed event publication must
// not add an unbounded wait to every catalog batch.
// After a timeout, back off and wait for pool capacity before trying again.
// Completion still records the durable result after the guard has been released.
func (r *Runner) libraryDeletionProgress(ctx context.Context, jobID string) func(int, int, string) {
	var retryAfter time.Time
	return func(current, total int, message string) {
		if ctx.Err() != nil {
			return
		}
		if !retryAfter.IsZero() {
			stats := r.repo.pool.Stat()
			if time.Now().Before(retryAfter) || stats.AcquiredConns() >= stats.MaxConns() {
				return
			}
		}
		progressCtx, cancel := context.WithTimeout(ctx, libraryDeletionProgressTimeout)
		defer cancel()
		defer func() {
			if progressCtx.Err() != nil {
				retryAfter = time.Now().Add(libraryDeletionProgressRetry)
			}
		}()
		if err := r.repo.UpdateProgress(progressCtx, jobID, current, total, message); err != nil {
			slog.WarnContext(progressCtx, "admin jobs: failed to update delete progress", "job_id", jobID, "error", err)
			return
		}
		r.publishJobByID(progressCtx, notifications.TypeJobProgress, jobID)
	}
}

func verifyCatalogSeedDigest(data []byte, expected string) error {
	if expected == "" {
		return nil
	}
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("catalog import source digest is invalid")
	}
	actual := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(actual[:], want) != 1 {
		return fmt.Errorf("catalog import source digest mismatch")
	}
	return nil
}

// bloemVerifyCatalogSeedSource checks a staged source against the digest its
// staged object key names (BloemStagedCatalogSeedKey), which the handler
// computed when it staged the download. A key without one, such as an
// uploaded source, is not checked. Silo's import streams its source, so a
// staged source is read once, at most the bounded compressed size, checked,
// and handed on as the checked bytes; *data still closes the original source.
func bloemVerifyCatalogSeedSource(data *io.ReadCloser, sourceKey string) error {
	expected := bloemStagedCatalogSeedDigest(sourceKey)
	if expected == "" {
		return nil
	}
	source := *data
	checked, err := io.ReadAll(io.LimitReader(source, catalogseed.MaxCompressedBundleBytes+1))
	if err != nil {
		return fmt.Errorf("reading catalog import source: %w", err)
	}
	if err := verifyCatalogSeedDigest(checked, expected); err != nil {
		return err
	}
	*data = struct {
		io.Reader
		io.Closer
	}{bytes.NewReader(checked), source}
	return nil
}

// bloemStagedCatalogSeedKeyPattern matches BloemStagedCatalogSeedKey's keys.
var bloemStagedCatalogSeedKeyPattern = regexp.MustCompile(`^catalog-seeds/imports/[0-9a-f]{32}/([0-9a-f]{64})\.json\.gz$`)

// BloemStagedCatalogSeedKey is the object key a remote catalog seed is staged
// under: a random nonce, then the SHA-256 of the staged bytes. The import job
// checks the object it reads against that digest, so the job request needs
// no field of its own for it.
func BloemStagedCatalogSeedKey(nonce [16]byte, digestHex string) string {
	return fmt.Sprintf("catalog-seeds/imports/%x/%s.json.gz", nonce, digestHex)
}

// bloemStagedCatalogSeedDigest returns the digest a staged key names, or ""
// for any other key.
func bloemStagedCatalogSeedDigest(key string) string {
	if m := bloemStagedCatalogSeedKeyPattern.FindStringSubmatch(key); m != nil {
		return m[1]
	}
	return ""
}
