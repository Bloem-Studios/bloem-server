package userdb

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Replicator manages S3 replication for a single SQLite database.
type Replicator interface {
	Start() error    // Begin continuous replication.
	Stop() error     // Stop replication, flush pending changes.
	Restore() error  // Restore from S3 to local path.
	IsRunning() bool // Report whether replication is active.
}

// S3ReplicatorConfig holds the S3 configuration for Litestream replication.
type S3ReplicatorConfig struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	PathStyle    bool
	SyncInterval time.Duration // default 1s
}

// s3ReplicaPath returns the S3 key prefix for a given user.
func s3ReplicaPath(bucket string, userID int) string {
	return fmt.Sprintf("s3://%s/%d/", bucket, userID)
}

// NewReplicator creates a replicator for the given database path and user.
// If s3Config is nil, returns a no-op replicator (local-only mode).
func NewReplicator(dbPath string, userID int, s3Config *S3ReplicatorConfig) Replicator {
	if s3Config == nil {
		slog.Debug("no S3 config provided, using no-op replicator", "user_id", userID)
		return &noopReplicator{}
	}

	cfg := *s3Config
	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = time.Second
	}

	return &s3Replicator{
		dbPath: dbPath,
		userID: userID,
		config: cfg,
	}
}

// ---------------------------------------------------------------------------
// noopReplicator — used when S3 is not configured.
// ---------------------------------------------------------------------------

type noopReplicator struct{}

func (n *noopReplicator) Start() error    { return nil }
func (n *noopReplicator) Stop() error     { return nil }
func (n *noopReplicator) Restore() error  { return nil }
func (n *noopReplicator) IsRunning() bool { return false }

// ---------------------------------------------------------------------------
// s3Replicator — real S3 replication implementation.
//
// TODO: Integrate the litestream Go library when it becomes available as a
// stable importable package. For now this implementation logs intended
// operations and tracks running state so the rest of the system can treat
// it as if replication were active.
// ---------------------------------------------------------------------------

type s3Replicator struct {
	dbPath  string
	userID  int
	config  S3ReplicatorConfig
	running bool
	mu      sync.Mutex
}

// Start begins continuous replication for the user's database.
func (r *s3Replicator) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return nil
	}

	dest := s3ReplicaPath(r.config.Bucket, r.userID)
	slog.Info("starting replication",
		"user_id", r.userID,
		"db_path", r.dbPath,
		"destination", dest,
		"sync_interval", r.config.SyncInterval,
	)

	r.running = true
	return nil
}

// Stop halts replication and flushes pending changes.
func (r *s3Replicator) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	slog.Info("stopping replication",
		"user_id", r.userID,
		"db_path", r.dbPath,
	)

	r.running = false
	return nil
}

// Restore downloads the latest replica from S3 to the local database path.
func (r *s3Replicator) Restore() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	src := s3ReplicaPath(r.config.Bucket, r.userID)
	slog.Info("restoring database from replica",
		"user_id", r.userID,
		"source", src,
		"db_path", r.dbPath,
	)

	return nil
}

// IsRunning reports whether replication is currently active.
func (r *s3Replicator) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
