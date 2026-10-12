package managedtracking

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"

	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
)

// Feedback changes only recommendation selection. It never rewrites watch state.
func (s *Service) ApplyRecommendationFeedback(ctx context.Context, id int, r *rec.RecommendationFeedback) (*rec.RecommendationFeedbackReceipt, error) {
	if s == nil || s.Pool == nil {
		return nil, ErrAuthority
	}
	if r == nil || !positiveID(r.AccountId) || !availabilityText(r.ProfileId) || !availabilityText(r.ContentId) || r.Revision == 0 || r.Revision > math.MaxInt64 {
		return nil, ErrSnapshot
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
	digest, err := source.Authority(ctx, p)
	if err != nil || digest == "" {
		return nil, ErrAuthority
	}
	items, err := source.Recommendations(ctx, p)
	if err != nil {
		return nil, err
	}
	eligible := false
	for _, item := range items {
		if item != nil && item.ContentId == r.ContentId {
			eligible = true
		}
	}
	after, err := source.Authority(ctx, p)
	if err != nil || after != digest {
		return nil, ErrAuthority
	}
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	current, err := authority(ctx, tx, id, grantShare)
	if err != nil {
		return nil, err
	}
	fresh, err := calendarProfile(ctx, tx, current.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	currentDigest, e := source.Authority(ctx, fresh)
	if e != nil || currentDigest != digest || current.Generation != info.Generation || current.Scope != info.Scope || fresh != p {
		return nil, ErrAuthority
	}
	userID, _ := strconv.Atoi(p.AccountID)
	// Serialize the logical lane even before its first row exists.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.rec.feedback:"+strconv.Itoa(id)+":"+p.AccountID+":"+p.ProfileID+":"+r.ContentId); err != nil {
		return nil, err
	}
	var revision uint64
	var dismissed bool
	err = tx.QueryRow(ctx, `SELECT revision,dismissed FROM bloem_managed_recommendation_feedback WHERE installation_id=$1 AND user_id=$2 AND profile_id=$3 AND content_id=$4 FOR UPDATE`, id, userID, p.ProfileID, r.ContentId).Scan(&revision, &dismissed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if revision == 0 && !eligible {
		return nil, ErrAuthority
	}
	if revision == r.Revision && dismissed != r.Dismissed {
		return nil, ErrConflict
	}
	if r.Revision > revision {
		_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_recommendation_feedback(installation_id,user_id,profile_id,content_id,revision,dismissed) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(installation_id,user_id,profile_id,content_id) DO UPDATE SET revision=EXCLUDED.revision,dismissed=EXCLUDED.dismissed,changed_at=clock_timestamp()`, id, userID, p.ProfileID, r.ContentId, int64(r.Revision), r.Dismissed)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &rec.RecommendationFeedbackReceipt{Revision: r.Revision, Dismissed: r.Dismissed}, nil
}
func (b *recommendationsBroker) Feedback(ctx context.Context, r *rec.RecommendationFeedback) (*rec.RecommendationFeedbackReceipt, error) {
	out, err := b.service.ApplyRecommendationFeedback(ctx, b.installation, r)
	return out, safeRPC(err)
}
