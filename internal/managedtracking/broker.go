package managedtracking

import (
	"context"
	"errors"
	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const Capability = "bloem.managed_tracking.v1"

type brokerService struct {
	managedv1.UnimplementedManagedTrackingHostServer
	service      *Service
	installation int
}

func (s *Service) RegisterBroker(server *grpc.Server, plugin string, installation int) {
	if s != nil && plugin == "bloem.pastime" {
		managedv1.RegisterManagedTrackingHostServer(server, &brokerService{service: s, installation: installation})
	}
}
func safeRPC(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrAuthority):
		return status.Error(codes.PermissionDenied, ErrAuthority.Error())
	case errors.Is(err, ErrSnapshot):
		return status.Error(codes.FailedPrecondition, ErrSnapshot.Error())
	case errors.Is(err, ErrConflict):
		return status.Error(codes.AlreadyExists, ErrConflict.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "managed_tracking_cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "managed_tracking_timeout")
	default:
		return status.Error(codes.Unavailable, "managed_tracking_unavailable")
	}
}
func (b *brokerService) GetInfo(ctx context.Context, _ *managedv1.InfoRequest) (*managedv1.InfoResponse, error) {
	info, err := b.service.GetInfo(ctx, b.installation)
	if err != nil {
		return nil, safeRPC(err)
	}
	return &managedv1.InfoResponse{Capability: Capability, InstanceId: info.InstanceID, EnrollmentScope: info.Scope, CredentialGeneration: info.Generation}, nil
}
func profileWire(p Profile) *managedv1.Profile {
	return &managedv1.Profile{AccountId: p.AccountID, ProfileId: p.ProfileID, DefaultProfileId: p.DefaultProfileID, AccountName: p.AccountName, Name: p.Name, Email: p.Email, AccountRevision: p.AccountRevision, Revision: p.Revision, AccountActive: p.AccountActive, Active: p.Active}
}
func profileDTO(p *managedv1.Profile) Profile {
	return Profile{AccountID: p.GetAccountId(), ProfileID: p.GetProfileId(), DefaultProfileID: p.GetDefaultProfileId(), AccountName: p.GetAccountName(), Name: p.GetName(), Email: p.GetEmail(), AccountRevision: p.GetAccountRevision(), Revision: p.GetRevision(), AccountActive: p.GetAccountActive(), Active: p.GetActive()}
}
func (b *brokerService) ListProfiles(ctx context.Context, r *managedv1.ListRequest) (*managedv1.ListResponse, error) {
	page, err := b.service.ListProfiles(ctx, b.installation, r.GetSnapshotId(), r.GetCursor(), int(r.GetLimit()))
	if err != nil {
		return nil, safeRPC(err)
	}
	out := &managedv1.ListResponse{InstanceId: page.InstanceID, SnapshotId: page.SnapshotID, NextCursor: page.NextCursor, Complete: page.Complete, Watermark: page.Watermark, CredentialGeneration: page.Generation}
	for _, p := range page.Profiles {
		out.Profiles = append(out.Profiles, profileWire(p))
	}
	return out, nil
}
func (b *brokerService) EnsureConnection(ctx context.Context, r *managedv1.EnrollmentRequest) (*managedv1.EnrollmentResponse, error) {
	if r.GetProfile() == nil || r.Profile.GetVerifiedIdentityIssuer() != "" || r.Profile.GetVerifiedIdentitySubject() != "" {
		return nil, safeRPC(ErrAuthority)
	}
	e := Enrollment{Profile: profileDTO(r.Profile), ProviderID: r.ProviderId, ID: r.EnrollmentId, DestinationAccountID: r.DestinationAccountId, DestinationProfileID: r.DestinationProfileId, Credential: r.Credential, Active: r.Active, Generation: r.CredentialGeneration}
	if err := b.service.EnsureConnection(ctx, b.installation, e); err != nil {
		return nil, safeRPC(err)
	}
	return &managedv1.EnrollmentResponse{EnrollmentId: e.ID, Ready: e.Active}, nil
}
func (b *brokerService) ReadChanges(ctx context.Context, r *managedv1.ChangesRequest) (*managedv1.ChangesResponse, error) {
	if r.GetLimit() < 1 || r.GetLimit() > 100 {
		return nil, status.Error(codes.InvalidArgument, "managed_tracking_limit_invalid")
	}
	pending, err := b.service.ReadChanges(ctx, b.installation)
	if err != nil {
		return nil, safeRPC(err)
	}
	return &managedv1.ChangesResponse{Pending: pending}, nil
}
func (b *brokerService) AckChanges(ctx context.Context, r *managedv1.AckRequest) (*managedv1.AckResponse, error) {
	if err := b.service.AckChanges(ctx, b.installation, r.GetWatermark()); err != nil {
		return nil, safeRPC(err)
	}
	return &managedv1.AckResponse{}, nil
}
func (b *brokerService) ResolveEvent(ctx context.Context, r *managedv1.EventRequest) (*managedv1.EventResponse, error) {
	a, err := b.service.ResolveEvent(ctx, b.installation, r.GetInstanceId(), r.GetAccountId(), r.GetProfileId(), r.GetEventId(), r.GetEventDigest())
	if err != nil {
		return nil, safeRPC(err)
	}
	return &managedv1.EventResponse{InstanceId: a.InstanceID, AccountId: a.AccountID, ProfileId: a.ProfileID, EventId: a.EventID, EntityId: a.EntityID, Revision: a.Revision, ConsumptionId: a.ConsumptionID, EventDigest: a.Digest}, nil
}
