package managedtracking

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
)

// AvailabilitySource checks catalog and file visibility using ordinary host policy.
type AvailabilitySource interface {
	LookupAvailability(context.Context, Profile, []*managedv1.AvailabilityTarget) ([]*managedv1.AvailabilityResult, error)
}

func availabilityText(s string) bool {
	return s != "" && len(s) <= 128 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func (s *Service) LookupAvailability(ctx context.Context, id int, r *managedv1.AvailabilityRequest) (*managedv1.AvailabilityResponse, error) {
	if r == nil || !positiveID(r.AccountId) || !availabilityText(r.ProfileId) || len(r.Targets) == 0 || len(r.Targets) > 100 {
		return nil, ErrSnapshot
	}
	seen := map[string]bool{}
	for _, t := range r.Targets {
		if t == nil || !availabilityText(t.RequestId) || seen[t.RequestId] || (t.Kind != "movie" && t.Kind != "series") || (t.Namespace != "tmdb" && t.Namespace != "imdb" && t.Namespace != "tvdb") || !availabilityText(t.ExternalId) {
			return nil, ErrSnapshot
		}
		seen[t.RequestId] = true
	}
	if s == nil || s.Pool == nil {
		return nil, ErrAuthority
	}
	source, ok := s.calendarSource().(AvailabilitySource)
	if !ok {
		return nil, ErrAuthority
	}
	// Do not hold grant locks over callbacks: revocation must be able to commit.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, id, grantRead)
	if err != nil {
		return nil, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	digest, err := s.calendarSource().Authority(ctx, p)
	if err != nil || digest == "" {
		return nil, ErrAuthority
	}
	checked := time.Now().UTC().Truncate(time.Second)
	result, err := source.LookupAvailability(ctx, p, r.Targets)
	if err != nil {
		return nil, err
	}
	if len(result) != len(r.Targets) {
		return nil, ErrSnapshot
	}
	for _, v := range result {
		if v == nil || !seen[v.RequestId] || (v.Available && (!availabilityText(v.ContentId))) || (!v.Available && v.ContentId != "") {
			return nil, ErrSnapshot
		}
		delete(seen, v.RequestId)
	}
	// A second fresh transaction fences publication against grant/profile changes.
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	nowInfo, err := authority(ctx, tx, id, grantShare)
	if err != nil {
		return nil, err
	}
	nowProfile, err := calendarProfile(ctx, tx, info.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	current, err := s.calendarSource().Authority(ctx, p)
	if err != nil || current != digest || nowInfo.Generation != info.Generation || nowProfile != p || !time.Now().Before(checked.Add(120*time.Second)) {
		return nil, ErrAuthority
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &managedv1.AvailabilityResponse{InstanceId: info.InstanceID, AccountId: r.AccountId, ProfileId: r.ProfileId, Results: result, CheckedAtUnix: checked.Unix(), ExpiresAtUnix: checked.Add(120 * time.Second).Unix()}, nil
}
func (b *brokerService) LookupAvailability(ctx context.Context, r *managedv1.AvailabilityRequest) (*managedv1.AvailabilityResponse, error) {
	out, err := b.service.LookupAvailability(ctx, b.installation, r)
	return out, safeRPC(err)
}
