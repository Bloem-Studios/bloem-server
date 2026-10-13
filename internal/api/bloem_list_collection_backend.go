package api

import (
	"context"
	"errors"
	"maps"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	lists "github.com/Silo-Server/silo-server/internal/managedtracking/listwire"
)

// This adapter translates the owned wire protocol into the durable publisher.
// Startup does not register it until requests, UI and full-chain gates pass.
type listCollectionBackend struct{ service *listcollections.Service }

var _ managedtracking.ListBackend = (*listCollectionBackend)(nil)

func listBackendError(err error) error {
	switch {
	case errors.Is(err, listcollections.ErrAuthority):
		return managedtracking.ErrAuthority
	case errors.Is(err, listcollections.ErrConflict):
		return managedtracking.ErrConflict
	case errors.Is(err, listcollections.ErrSnapshot), errors.Is(err, listcollections.ErrExpired):
		return managedtracking.ErrSnapshot
	default:
		return err
	}
}
func (b *listCollectionBackend) PollLists(ctx context.Context, tx pgx.Tx, installation int, info managedtracking.Info, limit uint32) (*lists.Jobs, error) {
	if b == nil || b.service == nil || limit < 1 || limit > 100 {
		return nil, managedtracking.ErrSnapshot
	}
	jobs, err := b.service.PollTx(ctx, tx, installation, info.InstanceID, int(limit))
	if err != nil {
		return nil, listBackendError(err)
	}
	out := &lists.Jobs{Jobs: make([]*lists.Job, 0, len(jobs))}
	for _, j := range jobs {
		out.Jobs = append(out.Jobs, &lists.Job{Id: j.ID, Mode: j.Mode, InstanceId: j.InstanceID, AccountId: j.AccountID, ProfileId: j.ProfileID, BindingId: j.BindingID, Locator: j.Locator, SourceId: j.SourceID, LeaseToken: j.LeaseToken, Generation: j.Generation, ExpiresAt: timestamppb.New(j.ExpiresAt), ResolutionToken: j.ResolutionToken})
	}
	return out, nil
}
func (b *listCollectionBackend) ResolvedList(ctx context.Context, tx pgx.Tx, installation int, _ managedtracking.Info, r *lists.Resolution) error {
	if b == nil || b.service == nil || r == nil || r.ExpiresAt == nil || r.ExpiresAt.CheckValid() != nil || r.Count > 10000 {
		return managedtracking.ErrSnapshot
	}
	return listBackendError(b.service.ResolvedTx(ctx, tx, installation, listcollections.Resolution{JobID: r.JobId, LeaseToken: r.LeaseToken, Generation: r.Generation, SourceID: r.SourceId, Name: r.Name, Kind: r.Kind, Count: int(r.Count), ResolutionToken: r.ResolutionToken, SigningKey: r.SigningKey, ExpiresAt: r.ExpiresAt.AsTime()}))
}
func listWirePage(r *lists.Page) (listcollections.Page, error) {
	if r == nil || r.Version != 1 || r.Total > 10000 || r.Position > r.Total || len(r.Entries) > 100 || len(r.Digest) != 32 || r.GeneratedAt == nil || r.ExpiresAt == nil || r.GeneratedAt.CheckValid() != nil || r.ExpiresAt.CheckValid() != nil {
		return listcollections.Page{}, managedtracking.ErrSnapshot
	}
	p := listcollections.Page{Version: int(r.Version), JobID: r.JobId, BindingID: r.BindingId, Generation: r.Generation, SourceID: r.SourceId, SnapshotID: r.SnapshotId, LeaseToken: r.LeaseToken, Position: int(r.Position), Total: int(r.Total), GeneratedAt: r.GeneratedAt.AsTime(), ExpiresAt: r.ExpiresAt.AsTime(), NextCursor: r.NextCursor, Final: r.Final, Entries: make([]listcollections.Entry, 0, len(r.Entries))}
	copy(p.Digest[:], r.Digest)
	for _, entry := range r.Entries {
		if entry == nil {
			return listcollections.Page{}, managedtracking.ErrSnapshot
		}
		p.Entries = append(p.Entries, listcollections.Entry{Kind: entry.Kind, Title: entry.Title, Year: int(entry.Year), ExternalIDs: maps.Clone(entry.ExternalIds), SourceEntryID: entry.SourceEntryId, UnsupportedReason: entry.UnsupportedReason})
	}
	return p, nil
}
func (b *listCollectionBackend) StageList(ctx context.Context, tx pgx.Tx, installation int, _ managedtracking.Info, r *lists.Page) error {
	if b == nil || b.service == nil {
		return managedtracking.ErrAuthority
	}
	p, err := listWirePage(r)
	if err != nil {
		return err
	}
	return listBackendError(b.service.StageTx(ctx, tx, installation, p))
}
func (b *listCollectionBackend) CompleteList(ctx context.Context, tx pgx.Tx, installation int, _ managedtracking.Info, r *lists.Completion) (*lists.Receipt, error) {
	if b == nil || b.service == nil || r == nil || len(r.Digest) != 32 {
		return nil, managedtracking.ErrSnapshot
	}
	in := listcollections.Completion{JobID: r.JobId, BindingID: r.BindingId, SnapshotID: r.SnapshotId, LeaseToken: r.LeaseToken, Generation: r.Generation, SourceAttestation: r.SourceAttestation}
	copy(in.Digest[:], r.Digest)
	receipt, err := b.service.CompleteTx(ctx, tx, installation, in)
	if err != nil {
		return nil, listBackendError(err)
	}
	return &lists.Receipt{JobId: receipt.JobID, BindingId: receipt.BindingID, TargetId: receipt.TargetID, Generation: receipt.Generation, Revision: receipt.Revision, Digest: receipt.Digest[:]}, nil
}
func (b *listCollectionBackend) RejectList(ctx context.Context, tx pgx.Tx, installation int, _ managedtracking.Info, r *lists.Failure) error {
	if b == nil || b.service == nil || r == nil {
		return managedtracking.ErrSnapshot
	}
	return listBackendError(b.service.RejectTx(ctx, tx, installation, r.JobId, r.LeaseToken, r.Generation, r.Reason))
}
