package listcollections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrRequestUnsupported = errors.New("unsupported native request identity")

type RequestOutcome struct {
	ID     string
	Status string
	Reason string
}
type RequestDomain interface {
	Submit(context.Context, Subject, Entry, string) (RequestOutcome, error)
	Lookup(context.Context, Subject, Entry) (RequestOutcome, error)
}
type DeliveryResult struct{ Delivered, Blocked, Retried, Cancelled int }

// RequestReplayKey uses the native filing identity, independent of source list,
// display metadata and supplemental IDs. Profiles and formats never share keys.
func RequestReplayKey(s Subject, e Entry) (string, error) {
	if s.UserID < 1 || s.ProfileID == "" || s.InstanceID == "" {
		return "", ErrAuthority
	}
	identity := ""
	switch e.Kind {
	case "movie", "series":
		id, err := strconv.Atoi(e.ExternalIDs["tmdb"])
		if err != nil || id < 1 {
			return "", ErrRequestUnsupported
		}
		identity = "tmdb:" + strconv.Itoa(id)
	case "ebook", "audiobook":
		isbn := e.ExternalIDs["isbn"]
		if isbn != "" && validISBN(isbn) {
			if len(isbn) == 10 {
				isbn = "978" + isbn[:9]
				sum := 0
				for i, c := range isbn {
					n := int(c - '0')
					if i%2 == 1 {
						n *= 3
					}
					sum += n
				}
				isbn += strconv.Itoa((10 - sum%10) % 10)
			}
			identity = "isbn:" + isbn
		} else if e.Kind == "audiobook" && len(e.ExternalIDs["asin"]) == 10 {
			identity = "asin:" + e.ExternalIDs["asin"]
		}
	default:
		return "", ErrRequestUnsupported
	}
	if identity == "" {
		return "", ErrRequestUnsupported
	}
	raw, _ := json.Marshal([]string{s.InstanceID, strconv.Itoa(s.UserID), s.ProfileID, e.Kind, identity})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// Installation lanes precede all authority locks. Dispatch retains a session
// lane, but commits every row-locking transaction before native service calls.
// This intentionally serializes bounded work for one installation across nodes.
func LaneTx(ctx context.Context, tx pgx.Tx, installation int) error {
	if installation < 1 {
		return ErrAuthority
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(147,$1::int4)`, installation)
	return err
}

func (s *Service) DeliverRequests(ctx context.Context, limit int) (DeliveryResult, error) {
	var out DeliveryResult
	if s == nil || s.Pool == nil || s.Requests == nil || limit < 1 || limit > 100 {
		return out, ErrAuthority
	}
	rows, err := s.Pool.Query(ctx, `SELECT i.binding_id::text,i.identity_key,b.installation_id FROM bloem_list_request_intents i JOIN bloem_list_bindings b ON b.id=i.binding_id WHERE i.wanted AND i.status IN('pending','running') AND i.next_due<=clock_timestamp() AND (i.lease_until IS NULL OR i.lease_until<=clock_timestamp()) ORDER BY i.next_due,i.binding_id,i.identity_key LIMIT $1`, limit)
	if err != nil {
		return out, err
	}
	type candidate struct {
		binding, key string
		installation int
	}
	work, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (candidate, error) {
		var c candidate
		err := row.Scan(&c.binding, &c.key, &c.installation)
		return c, err
	})
	if err != nil {
		return out, err
	}
	for _, c := range work {
		outcome, err := s.deliverRequest(ctx, c.binding, c.key, c.installation)
		if err != nil {
			return out, err
		}
		switch outcome {
		case "delivered":
			out.Delivered++
		case "blocked":
			out.Blocked++
		case "pending":
			out.Retried++
		case "cancelled":
			out.Cancelled++
		}
	}
	return out, nil
}

func (s *Service) deliverRequest(ctx context.Context, binding, key string, installation int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(147,$1::int4)`, installation).Scan(&locked); err != nil || !locked {
		return "", err
	}
	defer func() {
		// A pooled connection must never retain a session lock, even on cancellation.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.Exec(cleanup, `SELECT pg_advisory_unlock(147,$1::int4)`, installation); e != nil {
			conn.Hijack().Close(cleanup)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var subject Subject
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT subject FROM bloem_list_bindings WHERE id=$1 AND installation_id=$2`, binding, installation).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(raw, &subject); err != nil {
		return "", err
	}
	if err = s.authorize(ctx, tx, subject); err != nil {
		if !errors.Is(err, ErrAuthority) {
			return "", err
		}
		_, err = tx.Exec(ctx, `UPDATE bloem_list_request_intents SET status='blocked',reason='denied',lease_until=NULL WHERE binding_id=$1 AND identity_key=$2 AND wanted AND status<>'delivered'`, binding, key)
		if err != nil {
			return "", err
		}
		return "blocked", tx.Commit(ctx)
	}
	b, err := s.lockBinding(ctx, tx, binding)
	if err != nil {
		return "", err
	}
	var entryRaw []byte
	var generation uint64
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE bloem_list_request_intents SET status='running',attempts=attempts+1,lease_until=clock_timestamp()+interval '2 minutes' WHERE binding_id=$1 AND identity_key=$2 AND wanted AND status IN('pending','running') AND next_due<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp()) RETURNING entry,generation,attempts`, binding, key).Scan(&entryRaw, &generation, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var e Entry
	if err = json.Unmarshal(entryRaw, &e); err != nil {
		return "", err
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT CASE WHEN $2='personal' THEN EXISTS(SELECT 1 FROM user_personal_collections WHERE user_id=$3 AND id=$4 AND native AND collection_type='synced' AND source_config->>'bloem_list_binding'=$1) ELSE EXISTS(SELECT 1 FROM library_collections WHERE id=$4 AND collection_type='manual' AND source_config->>'bloem_list_binding'=$1) END`, binding, b.Scope, subject.UserID, b.TargetID).Scan(&exists)
	if err != nil {
		return "", err
	}
	outcome := RequestOutcome{}
	if !exists || b.Status == "detached" || b.Status == "paused" || generation != b.Generation {
		outcome = RequestOutcome{Status: "cancelled", Reason: "source_changed"}
	}
	if outcome.Status == "" {
		candidates, e2 := s.Domain.CandidatesTx(ctx, tx, subject, b, []Entry{e})
		if e2 != nil {
			if !errors.Is(e2, ErrAuthority) {
				return "", e2
			}
			outcome = RequestOutcome{Status: "blocked", Reason: "denied"}
		}
		matched, e2 := MatchEntries([]Entry{e}, candidates)
		if e2 != nil {
			return "", e2
		}
		if len(matched.ContentIDs) > 0 {
			outcome = RequestOutcome{Status: "cancelled", Reason: "available"}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	if outcome.Status == "" {
		replay, e2 := RequestReplayKey(subject, e)
		if e2 != nil {
			outcome = RequestOutcome{Status: "blocked", Reason: "unsupported"}
		} else {
			outcome, e2 = s.Requests.Lookup(ctx, subject, e)
			if e2 == nil && outcome.ID == "" && outcome.Status == "" {
				check, checkErr := conn.Begin(ctx)
				if checkErr != nil {
					return "", checkErr
				}
				authorized, checkErr := s.authorizeDelivery(ctx, check, subject, b, e, key, generation)
				if checkErr == nil {
					checkErr = check.Commit(ctx)
				}
				check.Rollback(ctx)
				if checkErr != nil {
					if !errors.Is(checkErr, ErrAuthority) {
						return "", checkErr
					}
					outcome = RequestOutcome{Status: "blocked", Reason: "denied"}
				} else if !authorized {
					outcome = RequestOutcome{Status: "cancelled", Reason: "source_changed"}
				} else {
					outcome, e2 = s.Requests.Submit(ctx, subject, e, replay)
				}
			}
			if e2 != nil {
				outcome = RequestOutcome{Status: "pending", Reason: "unavailable"}
			}
		}
	}
	if outcome.ID != "" {
		outcome.Status = "delivered"
	}
	if outcome.Status != "delivered" && outcome.Status != "blocked" && outcome.Status != "cancelled" {
		outcome.Status = "pending"
	}
	delay := time.Duration(1<<min(attempts, 10)) * time.Minute
	_, err = conn.Exec(ctx, `UPDATE bloem_list_request_intents SET status=$4,reason=$5,request_id=$6,lease_until=NULL,next_due=clock_timestamp()+$7::interval WHERE binding_id=$1 AND identity_key=$2 AND generation=$3 AND status='running'`, binding, key, generation, outcome.Status, outcome.Reason, outcome.ID, delay.String())
	return outcome.Status, err
}

// Lookup can wait on a native service. Recheck the managed source, target and
// wanted generation at the submission boundary after that wait.
func (s *Service) authorizeDelivery(ctx context.Context, tx pgx.Tx, subject Subject, b Binding, entry Entry, key string, generation uint64) (bool, error) {
	if err := s.authorize(ctx, tx, subject); err != nil {
		return false, err
	}
	current, err := s.lockBinding(ctx, tx, b.ID)
	if err != nil {
		return false, err
	}
	if current.Subject != subject || current.Generation != generation || current.Status == "detached" || current.Status == "paused" {
		return false, nil
	}
	candidates, err := s.Domain.CandidatesTx(ctx, tx, subject, current, []Entry{entry})
	if err != nil {
		return false, err
	}
	matches, err := MatchEntries([]Entry{entry}, candidates)
	if err != nil {
		return false, err
	}
	if len(matches.ContentIDs) > 0 {
		return false, nil
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_list_request_intents i WHERE binding_id=$1 AND identity_key=$2 AND generation=$3 AND wanted AND status='running' AND lease_until>clock_timestamp()) AND CASE WHEN $4='personal' THEN EXISTS(SELECT 1 FROM user_personal_collections c WHERE user_id=$5 AND id=$6 AND native AND collection_type='synced' AND source_config->>'bloem_list_binding'=$1::text) ELSE EXISTS(SELECT 1 FROM library_collections c WHERE id=$6 AND collection_type='manual' AND source_config->>'bloem_list_binding'=$1::text) END`, b.ID, key, generation, b.Scope, subject.UserID, b.TargetID).Scan(&allowed)
	return allowed, err
}
