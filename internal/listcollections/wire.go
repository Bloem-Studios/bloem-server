// Package listcollections owns source-backed Bloem collection synchronization.
package listcollections

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	ProtocolVersion  = 1
	MaxPageItems     = 100
	MaxSnapshotItems = 10000
	MaxSnapshotBytes = 8 << 20
	SnapshotLease    = 120 * time.Second
)

var ErrSnapshot = errors.New("invalid list snapshot")

// Hash uses lowercase SHA-256 hex on JSON boundaries, avoiding array-length
// coercion by encoding/json and keeping independently authored peers compatible.
type Hash [32]byte

func (h Hash) String() string               { return hex.EncodeToString(h[:]) }
func (h Hash) MarshalJSON() ([]byte, error) { return json.Marshal(h.String()) }
func (h *Hash) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ErrSnapshot
	}
	if len(s) != 64 {
		return ErrSnapshot
	}
	decoded, err := hex.DecodeString(s)
	if err != nil || hex.EncodeToString(decoded) != s {
		return ErrSnapshot
	}
	copy(h[:], decoded)
	return nil
}

type Entry struct {
	Kind              string            `json:"kind"`
	Title             string            `json:"title"`
	Year              int               `json:"year"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SourceEntryID     string            `json:"source_entry_id,omitempty"`
	UnsupportedReason string            `json:"unsupported_reason,omitempty"`
}

type Job struct {
	ID         string    `json:"id"`
	Mode       string    `json:"mode"`
	InstanceID string    `json:"instance_id"`
	AccountID  string    `json:"account_id"`
	ProfileID  string    `json:"profile_id"`
	BindingID  string    `json:"binding_id"`
	Locator    string    `json:"locator"`
	SourceID   string    `json:"source_id"`
	LeaseToken string    `json:"lease_token"`
	Generation uint64    `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type Page struct {
	Version     int       `json:"version"`
	JobID       string    `json:"job_id"`
	BindingID   string    `json:"binding_id"`
	Generation  uint64    `json:"generation"`
	SourceID    string    `json:"source_id"`
	SnapshotID  string    `json:"snapshot_id"`
	LeaseToken  string    `json:"lease_token"`
	Position    int       `json:"position"`
	Total       int       `json:"total"`
	Digest      Hash      `json:"digest"`
	GeneratedAt time.Time `json:"generated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	NextCursor  string    `json:"next_cursor"`
	Final       bool      `json:"final"`
	Entries     []Entry   `json:"entries"`
}

type Completion struct {
	JobID             string `json:"job_id"`
	BindingID         string `json:"binding_id"`
	SnapshotID        string `json:"snapshot_id"`
	LeaseToken        string `json:"lease_token"`
	SourceAttestation string `json:"source_attestation"`
	Generation        uint64 `json:"generation"`
	Digest            Hash   `json:"digest"`
}

type Receipt struct {
	JobID      string `json:"job_id"`
	BindingID  string `json:"binding_id"`
	TargetID   string `json:"target_id"`
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
	Digest     Hash   `json:"digest"`
}
