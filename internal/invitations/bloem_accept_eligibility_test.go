package invitations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

type bloemEligibilityRepository struct{ *fakeRepo }

func (r bloemEligibilityRepository) GetByTokenHashInTransaction(ctx context.Context, _ pgx.Tx, hash string) (*models.Invitation, error) {
	return r.GetByTokenHash(ctx, hash)
}

func (r bloemEligibilityRepository) AcceptInTransaction(context.Context, pgx.Tx, string, int) error {
	panic("ineligible invitation reached claim mutation")
}

func TestBloemAcceptInTransactionHidesIneligibleInvitations(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		inv  *models.Invitation
	}{
		{"unknown", nil},
		{"expired", &models.Invitation{ExpiresAt: now.Add(-time.Second)}},
		{"revoked", &models.Invitation{ExpiresAt: now.Add(time.Hour), RevokedAt: &now}},
		{"accepted", &models.Invitation{ExpiresAt: now.Add(time.Hour), AcceptedAt: &now}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := bloemEligibilityRepository{newFakeRepo()}
			if tc.inv != nil {
				repo.rows["digest"] = tc.inv
			}
			// No provisioner or session service: refusal must occur before writes.
			service := &Service{repo: repo, now: func() time.Time { return now }}
			pair, created, err := service.AcceptInTransaction(t.Context(), nil, "digest", "password", "device", "")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("accept error = %v, want ErrNotFound", err)
			}
			if pair != nil || created.User != nil {
				t.Fatal("ineligible invitation returned credentials or account")
			}
		})
	}
}
