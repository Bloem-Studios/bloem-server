package listcollections

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PollTx leases at most three jobs per installation. Database rows fence every
// later page; replicas skip claims already held by another publisher.
func (s *Service) PollTx(ctx context.Context, tx pgx.Tx, installation int, instance string, limit int) ([]Job, error) {
	out := []Job{}
	if limit < 1 || limit > 100 || installation < 1 {
		return out, ErrSnapshot
	}
	if s == nil || s.Domain == nil || s.Cipher == nil || tx == nil {
		return out, ErrAuthority
	}
	// Serialize admission across replicas, counting live leases rather than
	// bounding each RPC independently.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.list.poll:"+strconv.Itoa(installation)); err != nil {
		return out, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM bloem_list_jobs WHERE installation_id=$1 AND state='running' AND deadline>clock_timestamp()`, installation).Scan(&active); err != nil {
		return out, err
	}
	if active >= 3 {
		return out, nil
	}
	limit = min(limit, 3)
	limit = min(limit, 3-active)
	// Candidate reads hold no job locks: authority/binding must precede them.
	rows, err := tx.Query(ctx, `SELECT id::text,subject,binding_id::text FROM bloem_list_jobs WHERE installation_id=$1 AND state='pending' AND deadline>clock_timestamp() ORDER BY created_at,id LIMIT $2`, installation, limit*4)
	if err != nil {
		return out, err
	}
	type candidate struct {
		id      string
		raw     []byte
		binding *string
	}
	candidates, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidate, error) {
		var c candidate
		e := r.Scan(&c.id, &c.raw, &c.binding)
		return c, e
	})
	if err != nil {
		return out, err
	}
	for _, c := range candidates {
		if len(out) == limit {
			break
		}
		var subject Subject
		if err = json.Unmarshal(c.raw, &subject); err != nil {
			return out, err
		}
		if subject.InstallationID != installation || subject.InstanceID != instance {
			return out, ErrAuthority
		}
		authorityErr := s.authorize(ctx, tx, subject)
		if authorityErr != nil && !errors.Is(authorityErr, ErrAuthority) {
			return out, authorityErr
		}
		if c.binding != nil {
			var locked string
			err = tx.QueryRow(ctx, `SELECT id::text FROM bloem_list_bindings WHERE id=$1 FOR UPDATE SKIP LOCKED`, *c.binding).Scan(&locked)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return out, err
			}
		}
		var locked string
		err = tx.QueryRow(ctx, `SELECT id::text FROM bloem_list_jobs WHERE id=$1 AND state='pending' AND deadline>clock_timestamp() FOR UPDATE SKIP LOCKED`, c.id).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		if authorityErr != nil {
			if _, err = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='failed',failure='denied' WHERE id=$1`, c.id); err != nil {
				return out, err
			}
			if c.binding != nil {
				if _, err = tx.Exec(ctx, `UPDATE bloem_list_bindings SET status='paused',failure='denied' WHERE id=$1`, *c.binding); err != nil {
					return out, err
				}
			}
			continue
		}
		j, e := s.lockJob(ctx, tx, installation, c.id)
		if e != nil {
			return out, e
		}
		var locator, lease string
		if e = tx.QueryRow(ctx, `SELECT locator_cipher FROM bloem_list_jobs WHERE id=$1`, c.id).Scan(&locator); e != nil {
			return out, e
		}
		if locator != "" {
			j.job.Locator, e = s.Cipher.Decrypt(locator, "bloem.list.locator:"+j.job.ID)
			if e != nil {
				return out, e
			}
		}
		if j.binding != nil {
			var grant string
			if e = tx.QueryRow(ctx, `SELECT grant_cipher FROM bloem_list_bindings WHERE id=$1`, j.binding.ID).Scan(&grant); e != nil {
				return out, e
			}
			j.job.ResolutionToken, e = s.Cipher.Decrypt(grant, "bloem.list.grant:"+j.binding.ID)
			if e != nil {
				return out, e
			}
		}
		j.job.LeaseToken = uuid.NewString()
		lease, e = s.Cipher.Encrypt(j.job.LeaseToken, "bloem.list.lease:"+j.job.ID)
		if e != nil {
			return out, e
		}
		hash := sha256.Sum256([]byte(j.job.LeaseToken))
		if e = tx.QueryRow(ctx, `UPDATE bloem_list_jobs SET state='running',lease_cipher=$2,lease_digest=$3,claimed_at=clock_timestamp(),deadline=clock_timestamp()+interval '120 seconds' WHERE id=$1 RETURNING deadline`, j.job.ID, lease, hash[:]).Scan(&j.job.ExpiresAt); e != nil {
			return out, e
		}
		if j.binding != nil {
			if _, e = tx.Exec(ctx, `UPDATE bloem_list_bindings SET status='running' WHERE id=$1`, j.binding.ID); e != nil {
				return out, e
			}
		}
		out = append(out, j.job)
	}
	return out, nil
}

// QueueDue schedules bounded database-only work. External source fetching is
// performed later by the plugin. It is safe to call from multiple replicas.
func (s *Service) QueueDue(ctx context.Context, limit int) error {
	if s == nil || s.Pool == nil || s.Domain == nil || s.Cipher == nil {
		return ErrAuthority
	}
	if limit < 1 || limit > 100 {
		return ErrSnapshot
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,subject FROM bloem_list_bindings WHERE status NOT IN ('paused','detached') AND next_due<=clock_timestamp() ORDER BY next_due,id LIMIT $1`, limit)
	if err != nil {
		return err
	}
	type due struct {
		id  string
		raw []byte
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) { var d due; e := r.Scan(&d.id, &d.raw); return d, e })
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range items {
		var subject Subject
		if err = json.Unmarshal(item.raw, &subject); err != nil {
			return err
		}
		err = s.transaction(ctx, subject, func(tx pgx.Tx) error {
			var id string
			e := tx.QueryRow(ctx, `SELECT id::text FROM bloem_list_bindings WHERE id=$1 AND status NOT IN ('paused','detached') AND next_due<=clock_timestamp() FOR UPDATE SKIP LOCKED`, item.id).Scan(&id)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			b, e := s.lockBinding(ctx, tx, id)
			if e != nil {
				return e
			}
			if _, e = s.enqueueRefresh(ctx, tx, b); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `UPDATE bloem_list_bindings SET next_due=clock_timestamp()+interval '1 day' WHERE id=$1`, id)
			return e
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func pageAAD(job string, position int) string {
	return "bloem.list.page:" + job + ":" + strconv.Itoa(position)
}
