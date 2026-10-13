package managedtracking

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	lists "github.com/Silo-Server/silo-server/internal/managedtracking/listwire"
)

// ListBackend performs database-only work inside the installation grant's
// transaction. External source reads/attestation happen before these callbacks;
// callbacks must verify the persisted job, mapped profile and generation before
// touching staging or membership. No network or request dispatch belongs here.
type ListBackend interface {
	PollLists(context.Context, pgx.Tx, int, Info, uint32) (*lists.Jobs, error)
	ResolvedList(context.Context, pgx.Tx, int, Info, *lists.Resolution) error
	StageList(context.Context, pgx.Tx, int, Info, *lists.Page) error
	CompleteList(context.Context, pgx.Tx, int, Info, *lists.Completion) (*lists.Receipt, error)
	RejectList(context.Context, pgx.Tx, int, Info, *lists.Failure) error
}
type listBackendBox struct{ backend ListBackend }

// SetListBackend publishes an immutable adapter during startup composition.
func (s *Service) SetListBackend(b ListBackend) { s.listConfigured.Store(&listBackendBox{backend: b}) }
func (s *Service) listBackend() ListBackend {
	if s == nil {
		return nil
	}
	b := s.listConfigured.Load()
	if b == nil {
		return nil
	}
	return b.backend
}

type listBroker struct {
	lists.UnimplementedListHostServer
	service      *Service
	installation int
}

func (b *listBroker) Info(ctx context.Context, _ *lists.InfoRequest) (*lists.InfoResponse, error) {
	if b.service.listBackend() == nil {
		return nil, unsupportedList()
	}
	if _, err := b.service.GetInfo(ctx, b.installation); err != nil {
		return nil, safeRPC(err)
	}
	return &lists.InfoResponse{Capability: "bloem.list_sync.v1", Version: 1}, nil
}
func (b *listBroker) transaction(ctx context.Context, fn func(pgx.Tx, Info, ListBackend) error) error {
	s := b.service
	if s == nil || s.Pool == nil || s.Cipher == nil {
		return ErrAuthority
	}
	backend := s.listBackend()
	if backend == nil {
		return errListUnsupported
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, b.installation, grantShare)
	if err != nil {
		return err
	}
	if err = fn(tx, info, backend); err != nil {
		return err
	}
	// Backend callbacks fence the mapped profile and job. Recheck installation
	// enablement and organization status after their lock waits as well.
	fresh, err := authority(ctx, tx, b.installation, grantShare)
	if err != nil {
		return err
	}
	if fresh != info {
		return ErrAuthority
	}
	return tx.Commit(ctx)
}
func (b *listBroker) Poll(ctx context.Context, r *lists.PollRequest) (*lists.Jobs, error) {
	if r == nil || r.Limit < 1 || r.Limit > 100 {
		return nil, safeRPC(ErrSnapshot)
	}
	var out *lists.Jobs
	err := b.transaction(ctx, func(tx pgx.Tx, info Info, backend ListBackend) error {
		var err error
		out, err = backend.PollLists(ctx, tx, b.installation, info, r.Limit)
		if err != nil {
			return err
		}
		if out == nil || len(out.Jobs) > int(r.Limit) || proto.Size(out) > 1<<20 {
			return ErrSnapshot
		}
		seen := map[string]bool{}
		for _, j := range out.Jobs {
			if j == nil || j.InstanceId != info.InstanceID || j.Id == "" || seen[j.Id] || j.AccountId == "" || j.ProfileId == "" || j.Generation == 0 || j.LeaseToken == "" || j.ExpiresAt == nil || j.ExpiresAt.CheckValid() != nil {
				return ErrSnapshot
			}
			seen[j.Id] = true
		}
		return nil
	})
	return out, listRPCError(err)
}
func (b *listBroker) Resolved(ctx context.Context, r *lists.Resolution) (*lists.Ack, error) {
	if r == nil || r.JobId == "" || r.LeaseToken == "" || r.Generation == 0 || r.Count > 10000 || r.SourceId == "" || r.ResolutionToken == "" || r.ExpiresAt == nil || r.ExpiresAt.CheckValid() != nil || proto.Size(r) > 1<<20 {
		return nil, safeRPC(ErrSnapshot)
	}
	err := b.transaction(ctx, func(tx pgx.Tx, info Info, backend ListBackend) error {
		return backend.ResolvedList(ctx, tx, b.installation, info, r)
	})
	if err != nil {
		return nil, listRPCError(err)
	}
	return &lists.Ack{}, nil
}
func (b *listBroker) Stage(ctx context.Context, r *lists.Page) (*lists.Ack, error) {
	if r == nil || r.JobId == "" || r.LeaseToken == "" || r.Generation == 0 {
		return nil, safeRPC(ErrAuthority)
	}
	if r.Version != 1 || r.Total > 10000 || r.Position > r.Total || len(r.Entries) > 100 || len(r.Digest) != 32 || r.GeneratedAt == nil || r.ExpiresAt == nil || r.GeneratedAt.CheckValid() != nil || r.ExpiresAt.CheckValid() != nil || proto.Size(r) > 1<<20 {
		return nil, safeRPC(ErrSnapshot)
	}
	err := b.transaction(ctx, func(tx pgx.Tx, info Info, backend ListBackend) error {
		return backend.StageList(ctx, tx, b.installation, info, r)
	})
	if err != nil {
		return nil, listRPCError(err)
	}
	return &lists.Ack{}, nil
}
func (b *listBroker) Complete(ctx context.Context, r *lists.Completion) (*lists.Receipt, error) {
	if r == nil || r.JobId == "" || r.LeaseToken == "" || r.Generation == 0 {
		return nil, safeRPC(ErrAuthority)
	}
	if len(r.Digest) != 32 || r.SourceAttestation == "" || len(r.SourceAttestation) > 16384 || proto.Size(r) > 1<<20 {
		return nil, safeRPC(ErrSnapshot)
	}
	var out *lists.Receipt
	err := b.transaction(ctx, func(tx pgx.Tx, info Info, backend ListBackend) error {
		var err error
		out, err = backend.CompleteList(ctx, tx, b.installation, info, r)
		if err != nil {
			return err
		}
		if out == nil || out.JobId != r.JobId || out.BindingId != r.BindingId || out.Generation != r.Generation || len(out.Digest) != 32 || !bytes.Equal(out.Digest, r.Digest) || out.TargetId == "" || out.Revision == 0 {
			return ErrSnapshot
		}
		return nil
	})
	return out, listRPCError(err)
}
func (b *listBroker) Reject(ctx context.Context, r *lists.Failure) (*lists.Ack, error) {
	if r == nil || r.JobId == "" || r.LeaseToken == "" || r.Generation == 0 {
		return nil, safeRPC(ErrAuthority)
	}
	switch r.Reason {
	case "invalid_snapshot", "expired", "unsupported", "denied", "unavailable":
	default:
		return nil, safeRPC(ErrSnapshot)
	}
	err := b.transaction(ctx, func(tx pgx.Tx, info Info, backend ListBackend) error {
		return backend.RejectList(ctx, tx, b.installation, info, r)
	})
	if err != nil {
		return nil, listRPCError(err)
	}
	return &lists.Ack{}, nil
}

var errListUnsupported = errors.New("list_sync_unsupported")

func unsupportedList() error { return status.Error(codes.Unimplemented, "list_sync_unsupported") }
func listRPCError(err error) error {
	if errors.Is(err, errListUnsupported) {
		return unsupportedList()
	}
	return safeRPC(err)
}
