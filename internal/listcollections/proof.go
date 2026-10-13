package listcollections

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"time"
)

// SourceProof is an owned wire contract. The source signs the encoded JSON with
// Ed25519 over "bloem.list.proof.v1\\0" followed by the exact JSON bytes. The
// trusted installation's resolution supplies a public key which the binding
// pins; the source's private signing seed never leaves that source.
type SourceProof struct {
	Version    int       `json:"version"`
	InstanceID string    `json:"instance_id"`
	AccountID  string    `json:"account_id"`
	ProfileID  string    `json:"profile_id"`
	SourceID   string    `json:"source_id"`
	SnapshotID string    `json:"snapshot_id"`
	Digest     Hash      `json:"digest"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func VerifySourceProof(publicKey, token string, want SourceProof, now time.Time) error {
	if len(token) > 16384 || len(publicKey) != 43 {
		return ErrSnapshot
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return ErrSnapshot
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "lp1" {
		return ErrSnapshot
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return ErrSnapshot
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrSnapshot
	}
	if !ed25519.Verify(key, append([]byte("bloem.list.proof.v1\x00"), raw...), sig) {
		return ErrSnapshot
	}
	var got SourceProof
	if err = decodeStrict(raw, &got); err != nil {
		return ErrSnapshot
	}
	if want.Version != ProtocolVersion || got.Version != want.Version || got.InstanceID != want.InstanceID || got.AccountID != want.AccountID || got.ProfileID != want.ProfileID || got.SourceID != want.SourceID || got.SnapshotID != want.SnapshotID || got.Digest != want.Digest || got.Digest == (Hash{}) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		return ErrSnapshot
	}
	for _, s := range []string{got.InstanceID, got.AccountID, got.ProfileID, got.SourceID, got.SnapshotID} {
		if !requiredText(s, 256) {
			return ErrSnapshot
		}
	}
	if got.IssuedAt.IsZero() || got.IssuedAt.Before(want.IssuedAt) || got.IssuedAt.After(now) || !got.ExpiresAt.After(now) || !got.ExpiresAt.After(got.IssuedAt) || got.ExpiresAt.Sub(got.IssuedAt) > SnapshotLease {
		return ErrSnapshot
	}
	return nil
}
