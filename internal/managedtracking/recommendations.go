package managedtracking

import (
	"context"
	"strconv"
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
	userID, _ := strconv.Atoi(p.AccountID)
	hidden, err := tx.Query(ctx, `SELECT content_id FROM bloem_managed_recommendation_feedback WHERE installation_id=$1 AND user_id=$2 AND profile_id=$3 AND dismissed`, installation, userID, p.ProfileID)
	if err != nil {
		return nil, err
	}
	dismissed := map[string]bool{}
	for hidden.Next() {
		var content string
		if err = hidden.Scan(&content); err != nil {
			hidden.Close()
			return nil, err
		}
		dismissed[content] = true
	}
	err = hidden.Err()
	hidden.Close()
	if err != nil {
		return nil, err
	}
	visible := make([]*rec.Recommendation, 0, len(items))
	for _, item := range items {
		if !dismissed[item.ContentId] {
			visible = append(visible, item)
		}
	}
	items = visible
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
