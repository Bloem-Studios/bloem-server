// Package managedtracking owns installation-scoped native tracking authority.
package managedtracking

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAuthority = errors.New("managed_tracking_authority_denied")
var ErrSnapshot = errors.New("managed_tracking_snapshot_invalid")

type Service struct {
	Pool   *pgxpool.Pool
	Cipher *secret.Cipher
}
type Profile struct {
	AccountID        string `json:"account_id"`
	ProfileID        string `json:"profile_id"`
	DefaultProfileID string `json:"default_profile_id"`
	AccountName      string `json:"account_name"`
	Name             string `json:"name"`
	Email            string `json:"email"`
	AccountRevision  uint64 `json:"account_revision"`
	Revision         uint64 `json:"revision"`
	AccountActive    bool   `json:"account_active"`
	Active           bool   `json:"active"`
}
type Info struct {
	InstanceID, Scope string
	Generation        uint64
}
type Page struct {
	Info
	SnapshotID, NextCursor, Watermark string
	Complete                          bool
	Profiles                          []Profile
}

// Grant is an operator/backend API, never called through a plugin broker.
// It binds a concrete installation to one tenant; changing its scope requires
// revocation and an explicit new enrollment rather than silently reusing grants.
func (s *Service) Grant(ctx context.Context, installation int, tenant string) error {
	if installation < 1 {
		return ErrAuthority
	}
	if _, err := uuid.Parse(tenant); err != nil {
		return ErrAuthority
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO bloem_managed_grants(installation_id,tenant_id)
 SELECT i.id,o.id FROM plugin_installations i CROSS JOIN organizations o
 WHERE i.id=$1 AND i.enabled AND i.kind='plugin' AND i.plugin_id='bloem.pastime' AND o.id=$2 AND o.status='active'
 ON CONFLICT(installation_id) DO UPDATE SET active=true,
 generation=CASE WHEN bloem_managed_grants.active THEN bloem_managed_grants.generation ELSE bloem_managed_grants.generation+1 END,
 renewed_at=CASE WHEN bloem_managed_grants.active THEN bloem_managed_grants.renewed_at ELSE clock_timestamp() END,
 acked_revision=CASE WHEN bloem_managed_grants.active THEN bloem_managed_grants.acked_revision ELSE 0 END
 WHERE bloem_managed_grants.tenant_id=EXCLUDED.tenant_id`, installation, tenant)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAuthority
	}
	return nil
}
func authority(ctx context.Context, tx pgx.Tx, installation int) (Info, error) {
	var out Info
	err := tx.QueryRow(ctx, `SELECT g.instance_id::text,g.tenant_id::text,g.generation FROM bloem_managed_grants g
 JOIN plugin_installations i ON i.id=g.installation_id JOIN organizations o ON o.id=g.tenant_id
 WHERE g.installation_id=$1 AND g.active AND i.enabled AND i.kind='plugin' AND i.plugin_id='bloem.pastime' AND o.status='active'
 FOR UPDATE OF g`, installation).Scan(&out.InstanceID, &out.Scope, &out.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrAuthority
	}
	return out, err
}
func (s *Service) GetInfo(ctx context.Context, installation int) (Info, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Info{}, err
	}
	defer tx.Rollback(ctx)
	out, err := authority(ctx, tx, installation)
	if err != nil {
		return out, err
	}
	var renew bool
	if err = tx.QueryRow(ctx, `SELECT renewed_at<clock_timestamp()-interval '30 days' FROM bloem_managed_grants WHERE installation_id=$1`, installation).Scan(&renew); err != nil {
		return out, err
	}
	if renew {
		if err = tx.QueryRow(ctx, `UPDATE bloem_managed_grants SET generation=generation+1,renewed_at=clock_timestamp(),acked_revision=0 WHERE installation_id=$1 RETURNING generation`, installation).Scan(&out.Generation); err != nil {
			return out, err
		}
		if err = disableConnections(ctx, tx, installation); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

func (s *Service) ListProfiles(ctx context.Context, installation int, snapshot, cursor string, limit int) (Page, error) {
	out := Page{Profiles: []Profile{}}
	if limit < 1 || limit > 100 || snapshot == "" && cursor != "" {
		return out, ErrSnapshot
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	// Acquire publication fence before reading grant/source projection. Trigger
	// writers lock this row, so watermark and copied records describe one commit.
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton FOR UPDATE`).Scan(&revision); err != nil {
		return out, err
	}
	out.Info, err = authority(ctx, tx, installation)
	if err != nil {
		return out, err
	}
	offset := 0
	if snapshot == "" {
		snapshot = uuid.NewString()
		_, err = tx.Exec(ctx, `DELETE FROM bloem_managed_snapshots WHERE installation_id=$1 AND expires_at<clock_timestamp()`, installation)
		if err != nil {
			return out, err
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_managed_snapshots WHERE installation_id=$1`, installation).Scan(&count); err != nil {
			return out, err
		}
		if count >= 16 {
			return out, ErrSnapshot
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_snapshots(id,installation_id,generation,watermark) VALUES($1,$2,$3,$4)`, snapshot, installation, out.Generation, revision)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_snapshot_rows(snapshot_id,ordinal,profile)
 SELECT $1,row_number() OVER(ORDER BY account_id,(profile_id=default_profile_id) DESC,profile_id),jsonb_build_object(
 'account_id',account_id::text,'profile_id',profile_id,'default_profile_id',default_profile_id,
 'account_name',account_name,'name',name,'email',email,'account_revision',account_revision,'revision',revision,
 'account_active',account_active,'active',active) FROM bloem_managed_profiles WHERE tenant_id=$2`, snapshot, out.Scope)
		if err != nil {
			return out, err
		}
	} else {
		if _, err = uuid.Parse(snapshot); err != nil {
			return out, ErrSnapshot
		}
		// Cursor is opaque and carries a random, snapshot-bound issued token plus
		// offset; accepting a manufactured offset would allow skipping pages.
		if cursor != "" {
			err = tx.QueryRow(ctx, `SELECT ordinal FROM bloem_managed_page_cursors WHERE snapshot_id=$1 AND token=$2`, snapshot, cursor).Scan(&offset)
			if err != nil {
				return out, ErrSnapshot
			}
		}
	}
	var generation uint64
	err = tx.QueryRow(ctx, `SELECT generation,ack_token::text FROM bloem_managed_snapshots WHERE id=$1 AND installation_id=$2 AND expires_at>clock_timestamp() FOR UPDATE`, snapshot, installation).Scan(&generation, &out.Watermark)
	if err != nil || generation != out.Generation {
		return out, ErrSnapshot
	}
	out.SnapshotID = snapshot
	rows, err := tx.Query(ctx, `SELECT ordinal,profile FROM bloem_managed_snapshot_rows WHERE snapshot_id=$1 AND ordinal>$2 ORDER BY ordinal LIMIT $3`, snapshot, offset, limit+1)
	if err != nil {
		return out, err
	}
	last := offset
	more := false
	for rows.Next() {
		var n int
		var b []byte
		if err = rows.Scan(&n, &b); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Profiles) == limit {
			more = true
			break
		}
		var p Profile
		if err = json.Unmarshal(b, &p); err != nil {
			rows.Close()
			return out, err
		}
		out.Profiles = append(out.Profiles, p)
		last = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Complete = !more
	if more {
		out.NextCursor = uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_page_cursors(snapshot_id,token,ordinal) VALUES($1,$2,$3)`, snapshot, out.NextCursor, last)
	} else {
		_, err = tx.Exec(ctx, `UPDATE bloem_managed_snapshots SET delivered=true WHERE id=$1`, snapshot)
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Service) ReadChanges(ctx context.Context, installation int) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation)
	if err != nil {
		return false, err
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT g.renewed_at<clock_timestamp()-interval '30 days' OR EXISTS(SELECT 1 FROM bloem_managed_changes c JOIN bloem_managed_grants g ON g.installation_id=$1 WHERE c.tenant_id=$2 AND c.revision>g.acked_revision) FROM bloem_managed_grants g WHERE g.installation_id=$1`, installation, info.Scope).Scan(&pending)
	if err != nil {
		return false, err
	}
	return pending, tx.Commit(ctx)
}
func (s *Service) AckChanges(ctx context.Context, installation int, watermark string) error {
	if _, err := uuid.Parse(watermark); err != nil {
		return ErrSnapshot
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_managed_grants g SET acked_revision=greatest(g.acked_revision,s.watermark)
 FROM bloem_managed_snapshots s WHERE g.installation_id=$1 AND s.installation_id=g.installation_id AND s.ack_token=$2 AND s.generation=$3 AND s.delivered AND s.expires_at>clock_timestamp()`, installation, watermark, info.Generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSnapshot
	}
	return tx.Commit(ctx)
}
