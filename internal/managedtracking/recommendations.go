package managedtracking

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
)

// RecommendationsSource reuses the host's cached AI projection and current policy.
// It is optional on the immutable calendar adapter to avoid another startup seam.
type RecommendationsSource interface {
	CalendarSource
	Recommendations(context.Context, Profile) ([]*rec.Recommendation, error)
}

func (s *Service) ReadRecommendations(ctx context.Context, installation int, r *rec.RecommendationsRequest) (*rec.RecommendationsFeed, error) {
	if s == nil || s.Pool == nil || r == nil || !positiveID(r.AccountId) || r.ProfileId == "" || len(r.ProfileId) > 128 {
		return nil, ErrAuthority
	}
	source, ok := s.calendarSource().(RecommendationsSource)
	if !ok {
		return nil, ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation, grantShare)
	if err != nil {
		return nil, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	before, err := source.Authority(ctx, p)
	if err != nil {
		return nil, err
	}
	if before == "" {
		return nil, ErrAuthority
	}
	var generated time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&generated); err != nil {
		return nil, err
	}
	items, err := source.Recommendations(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(items) > 100 {
		return nil, ErrSnapshot
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item == nil || item.ContentId == "" || item.Title == "" || (item.Kind != "movie" && item.Kind != "series") || seen[item.ContentId] {
			return nil, ErrSnapshot
		}
		seen[item.ContentId] = true
	}
	after, err := source.Authority(ctx, p)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, ErrAuthority
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &rec.RecommendationsFeed{InstanceId: info.InstanceID, AccountId: p.AccountID, ProfileId: p.ProfileID, GeneratedAt: timestamppb.New(generated), ExpiresAt: timestamppb.New(generated.Add(120 * time.Second)), Items: items}, nil
}

type recommendationsBroker struct {
	rec.UnimplementedRecommendationsHostServer
	service      *Service
	installation int
}

func (b *recommendationsBroker) Read(ctx context.Context, r *rec.RecommendationsRequest) (*rec.RecommendationsFeed, error) {
	out, err := b.service.ReadRecommendations(ctx, b.installation, r)
	return out, safeRPC(err)
}
