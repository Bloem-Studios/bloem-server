package handlers

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// Existing Bloem handler fixtures implement the upstream atomic mutation seam.
func (r *customRoleAdminUserRepo) GetAdminSnapshot(ctx context.Context, id int) (auth.AdminUserSnapshot, error) {
	u, e := r.GetByID(ctx, id)
	return auth.AdminUserSnapshot{User: u}, e
}
func (r *customRoleAdminUserRepo) MutateAdminAccount(ctx context.Context, id int, _ int64, input *models.UpdateUserInput, validate func(*models.User, pgx.Tx) (bool, error)) (auth.AdminUserSnapshot, error) {
	s, e := r.GetAdminSnapshot(ctx, id)
	if e != nil {
		return s, e
	}
	if _, e = validate(s.User, nil); e != nil {
		return s, e
	}
	if input == nil {
		e = r.Delete(ctx, id)
	} else {
		e = r.Update(ctx, id, *input)
	}
	return s, e
}
func (r *cancellationAdminUserRepo) GetAdminSnapshot(ctx context.Context, id int) (auth.AdminUserSnapshot, error) {
	u, e := r.GetByID(ctx, id)
	return auth.AdminUserSnapshot{User: u}, e
}
func (r *cancellationAdminUserRepo) MutateAdminAccount(ctx context.Context, id int, _ int64, input *models.UpdateUserInput, validate func(*models.User, pgx.Tx) (bool, error)) (auth.AdminUserSnapshot, error) {
	s, e := r.GetAdminSnapshot(ctx, id)
	if e != nil {
		return s, e
	}
	if _, e = validate(s.User, nil); e != nil {
		return s, e
	}
	if input == nil {
		e = r.Delete(ctx, id)
	} else {
		e = r.Update(ctx, id, *input)
	}
	return s, e
}
