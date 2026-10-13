package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/listcollections"
)

func (r *ListCollectionRuntime) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || !strings.EqualFold(u.Scheme+"://"+u.Host, r.origin) {
		return listcollections.ErrSnapshot
	}
	return nil
}
func (r *ListCollectionRuntime) Subject(ctx context.Context, user int, profile string, installation int, id, resource string) (listcollections.Subject, error) {
	var s listcollections.Subject
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			return s, listcollections.ErrSnapshot
		}
		query := `SELECT subject FROM bloem_list_bindings WHERE id=$1 AND user_id=$2 AND profile_id=$3`
		if resource == "preview" {
			query = `SELECT subject FROM bloem_list_jobs WHERE id=$1 AND mode='resolve' AND subject->>'UserID'=$2::int::text AND subject->>'ProfileID'=$3`
		}
		var raw []byte
		err := r.service.Pool.QueryRow(ctx, query, id, user, profile).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return s, listcollections.ErrAuthority
		}
		if err != nil {
			return s, err
		}
		if err = json.Unmarshal(raw, &s); err != nil {
			return s, err
		}
		if s.UserID != user || s.ProfileID != profile {
			return s, listcollections.ErrAuthority
		}
	} else {
		info, err := r.managed.GetInfo(ctx, installation)
		if err != nil {
			return s, listcollections.ErrAuthority
		}
		s = listcollections.Subject{UserID: user, ProfileID: profile, InstallationID: installation, InstanceID: info.InstanceID}
	}
	var role string
	if err := r.service.Pool.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND enabled`, user).Scan(&role); err != nil {
		return s, listcollections.ErrAuthority
	}
	s.IsAdmin = role == "admin"
	return s, nil
}
func (r *ListCollectionRuntime) Connections(ctx context.Context, user int, profile string) ([]handlers.BloemListConnection, error) {
	out := []handlers.BloemListConnection{}
	rows, err := r.service.Pool.Query(ctx, `SELECT g.installation_id FROM bloem_managed_grants g JOIN plugin_installations i ON i.id=g.installation_id JOIN bloem_managed_enrollments e ON e.installation_id=g.installation_id WHERE g.active AND i.enabled AND i.plugin_id='bloem.pastime' AND e.active AND e.account_id=$1 AND e.profile_id=$2 ORDER BY g.installation_id`, user, profile)
	if err != nil {
		return out, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		s, err := r.Subject(ctx, user, profile, id, "", "preview")
		if errors.Is(err, listcollections.ErrAuthority) {
			continue
		}
		if err != nil {
			return out, err
		}
		tx, err := r.service.Pool.Begin(ctx)
		if err != nil {
			return out, err
		}
		err = r.domain.AuthorizeTx(ctx, tx, s)
		serverAllowed := false
		if err == nil {
			serverErr := r.domain.serverAuthorityTx(ctx, tx, s)
			serverAllowed = serverErr == nil
			if serverErr != nil && !errors.Is(serverErr, listcollections.ErrAuthority) {
				err = serverErr
			}
		}
		tx.Rollback(ctx)
		if errors.Is(err, listcollections.ErrAuthority) {
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, handlers.BloemListConnection{InstallationID: id, Origin: r.origin, CanCreateServer: serverAllowed})
	}
	return out, nil
}
func newBloemListCollections(deps Dependencies) *handlers.BloemListCollectionsHandler {
	h := &handlers.BloemListCollectionsHandler{}
	if r := deps.ListCollections; r != nil && r.service != nil && r.service.Requests != nil {
		h.Service = r.service
		h.Backend = r
	}
	return h
}
func mountBloemListCollections(r chi.Router, h *handlers.BloemListCollectionsHandler, client bloemClientSurface) {
	if client.auth == nil || client.tenant == nil || client.viewer == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(client.auth.RequireAuth, client.tenant.ResolveNative)
		if client.rateLimit != nil {
			r.Use(client.rateLimit.Handler)
		}
		r.Use(client.viewer.RequireViewerAccess, apimw.RequireProfile)
		r.Route("/list-collections", func(r chi.Router) {
			r.Get("/capabilities", h.HandleCapabilities)
			r.Post("/previews", h.HandlePreview)
			r.Get("/previews/{id}", h.HandleGetPreview)
			r.Post("/bindings", h.HandleCreate)
			r.Get("/bindings/{id}", h.HandleGet)
			r.Post("/bindings/{id}/refresh", h.HandleRefresh)
			r.Post("/bindings/{id}/disconnect", h.HandleDisconnect)
		})
	})
}
