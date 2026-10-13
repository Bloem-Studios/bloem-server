package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

type LibraryMutationSource interface {
	LibrarySource
	MutateLibrary(context.Context, Profile, pgx.Tx, *library.LibraryMutation) (*library.LibraryMutationReceipt, error)
}

// MutateLibrary commits the native domain effect and the exact replay receipt
// in one PostgreSQL transaction. A delivery failure cannot duplicate creation.
func (s *Service) MutateLibrary(ctx context.Context, id int, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	if s == nil || s.Pool == nil || in == nil || !positiveID(in.AccountId) || !availabilityText(in.ProfileId) {
		return nil, ErrAuthority
	}
	if !availabilityText(in.EventId) || len(in.Name) > 200 || len(in.Description) > 10000 || in.ExpectedRevision > math.MaxInt64 || len(in.Items) > 100 || proto.Size(in) > 1<<20 {
		return nil, ErrSnapshot
	}
	source, ok := s.calendarSource().(LibraryMutationSource)
	if !ok {
		return nil, ErrAuthority
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(in)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, id, grantRead)
	if err != nil {
		return nil, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, in.AccountId, in.ProfileId)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	before, err := source.LibraryAuthority(ctx, p)
	if err != nil {
		return nil, err
	}
	if before == "" {
		return nil, ErrAuthority
	}
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	freshInfo, err := authority(ctx, tx, id, grantShare)
	if err != nil {
		return nil, err
	}
	fresh, err := calendarProfile(ctx, tx, freshInfo.Scope, in.AccountId, in.ProfileId)
	if err != nil {
		return nil, err
	}
	now, err := source.LibraryAuthority(ctx, fresh)
	if err != nil {
		return nil, err
	}
	if freshInfo != info || fresh != p || now != before {
		return nil, ErrAuthority
	}
	// The event lane also serializes a first delivery before its receipt exists.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.library.event:"+strconv.Itoa(id)+":"+p.AccountID+":"+p.ProfileID+":"+in.EventId); err != nil {
		return nil, err
	}
	var priorDigest, receipt []byte
	err = tx.QueryRow(ctx, `SELECT digest,receipt FROM bloem_managed_library_receipts WHERE installation_id=$1 AND user_id=$2 AND profile_id=$3 AND event_id=$4`, id, p.AccountID, p.ProfileID, in.EventId).Scan(&priorDigest, &receipt)
	out := new(library.LibraryMutationReceipt)
	if err == nil {
		if !bytes.Equal(priorDigest, digest[:]) {
			return nil, ErrConflict
		}
		if err = proto.Unmarshal(receipt, out); err != nil {
			return nil, err
		}
	} else {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out, err = source.MutateLibrary(ctx, p, tx, in)
		if err != nil {
			return nil, err
		}
		if out == nil || out.EventId != in.EventId || !availabilityText(out.TargetId) || out.Revision == 0 || out.Revision > math.MaxInt64 {
			return nil, ErrSnapshot
		}
		switch out.Status {
		case "applied", "no_change", "removed":
		default:
			return nil, ErrSnapshot
		}
		receipt, err = proto.Marshal(out)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_managed_library_receipts(installation_id,user_id,profile_id,event_id,digest,receipt) VALUES($1,$2,$3,$4,$5,$6)`, id, p.AccountID, p.ProfileID, in.EventId, digest[:], receipt); err != nil {
			return nil, err
		}
	}
	after, err := source.LibraryAuthority(ctx, p)
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, ErrAuthority
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (b *libraryBroker) Mutate(ctx context.Context, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	out, err := b.service.MutateLibrary(ctx, b.installation, in)
	return out, safeRPC(err)
}
