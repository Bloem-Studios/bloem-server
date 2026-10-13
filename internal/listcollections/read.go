package listcollections

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type PreviewStatus struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Count     int       `json:"count"`
	ExpiresAt time.Time `json:"expires_at"`
	Failure   string    `json:"failure"`
}
type RequestCounts struct {
	Pending   int `json:"pending"`
	Blocked   int `json:"blocked"`
	Delivered int `json:"delivered"`
	Cancelled int `json:"cancelled"`
	Quota     int `json:"quota"`
}

func (s *Service) GetPreview(ctx context.Context, subject Subject, id string) (PreviewStatus, error) {
	var out PreviewStatus
	if !validID(id) {
		return out, ErrSnapshot
	}
	err := s.transaction(ctx, subject, func(tx pgx.Tx) error {
		var raw []byte
		var resolution []byte
		err := tx.QueryRow(ctx, `SELECT id::text,subject,state,deadline,failure,resolution FROM bloem_list_jobs WHERE id=$1 AND installation_id=$2 AND mode='resolve'`, id, subject.InstallationID).Scan(&out.ID, &raw, &out.Status, &out.ExpiresAt, &out.Failure, &resolution)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAuthority
		}
		if err != nil {
			return err
		}
		var owner Subject
		if err = json.Unmarshal(raw, &owner); err != nil {
			return err
		}
		if owner != subject {
			return ErrAuthority
		}
		if len(resolution) > 0 {
			var r Resolution
			if err = json.Unmarshal(resolution, &r); err != nil {
				return err
			}
			out.Name, out.Kind, out.Count = r.Name, r.Kind, r.Count
			if r.ExpiresAt.Before(out.ExpiresAt) {
				out.ExpiresAt = r.ExpiresAt
			}
		}
		now, err := dbNow(ctx, tx)
		if err != nil {
			return err
		}
		if !out.ExpiresAt.After(now) && out.Status != "failed" {
			out.Status = "expired"
		}
		return nil
	})
	return out, err
}
func (s *Service) GetBinding(ctx context.Context, subject Subject, id string) (Binding, RequestCounts, error) {
	var out Binding
	var counts RequestCounts
	if !validID(id) || s == nil || s.Domain == nil {
		return out, counts, ErrSnapshot
	}
	err := s.transactionAuthorized(ctx, subject, s.Domain.AuthorizeDetachTx, func(tx pgx.Tx) error {
		b, err := s.lockBinding(ctx, tx, id)
		if err != nil {
			return err
		}
		owner := b.Subject
		owner.IsAdmin = subject.IsAdmin
		if owner != subject || b.Scope == "server" && !subject.IsAdmin {
			return ErrAuthority
		}
		out = b
		return tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE wanted AND status IN('pending','running')),count(*) FILTER(WHERE wanted AND status='blocked'),count(*) FILTER(WHERE status='delivered'),count(*) FILTER(WHERE status='cancelled'),count(*) FILTER(WHERE wanted AND status='blocked' AND reason='quota') FROM bloem_list_request_intents WHERE binding_id=$1`, id).Scan(&counts.Pending, &counts.Blocked, &counts.Delivered, &counts.Cancelled, &counts.Quota)
	})
	return out, counts, err
}
