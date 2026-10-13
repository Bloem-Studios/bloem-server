package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
)

type listCollectionMutationGuard struct{ pool *pgxpool.Pool }

func (g listCollectionMutationGuard) check(ctx context.Context, scope string, user int, id, operation string, fields map[string]json.RawMessage) error {
	if g.pool == nil || id == "" {
		return nil
	}
	query := `SELECT COALESCE(source_config->>'bloem_list_binding','') FROM user_personal_collections WHERE user_id=$1 AND id=$2 AND native`
	args := []any{user, id}
	if scope == "server" {
		query = `SELECT COALESCE(source_config->>'bloem_list_binding','') FROM library_collections WHERE id=$1`
		args = []any{id}
	}
	var binding string
	err := g.pool.QueryRow(ctx, query, args...).Scan(&binding)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && binding == "" {
		return nil
	}
	if err != nil {
		return &handlers.APIError{Status: 503, Code: "list_authority_unavailable", Message: "Collection authority unavailable"}
	}
	if operation == "update" {
		for _, field := range []string{"source_url", "source_config", "collection_type", "library_id", "library_ids", "management_mode", "management_source", "management_key", "query_definition", "display_query_definition", "sort_config", "sync_schedule", "max_items"} {
			if _, present := fields[field]; present {
				return linkedListMutationError()
			}
		}
		return nil
	}
	return linkedListMutationError()
}
func linkedListMutationError() error {
	return &handlers.APIError{Status: 409, Code: "collection_source_managed", Message: "Disconnect the source before editing linked membership or source settings"}
}

// v1 uses the native concrete HTTP handlers. This hook runs after their existing
// authentication/profile gates and retains the original body for native decoding.
func bloemListMutationMiddleware(pool *pgxpool.Pool, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			// A mounted Chi group's middleware runs before its child route has
			// populated {id}. Resolve only this group's collection path segment.
			if id == "" {
				parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
				for i, part := range parts {
					if part == "collections" && i+1 < len(parts) {
						id = parts[i+1]
					}
				}
				if len(parts) == 1 {
					id = parts[0]
				}
			}
			if pool == nil || id == "" || strings.Contains(r.URL.Path, "/groups/") || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodDelete && !strings.Contains(r.URL.Path, "/items/") {
				next.ServeHTTP(w, r)
				return
			}
			claims := apimw.GetClaims(r.Context())
			if claims == nil {
				next.ServeHTTP(w, r)
				return
			}
			operation := "update"
			if strings.Contains(r.URL.Path, "/items/") {
				operation = "members"
			} else if strings.HasSuffix(r.URL.Path, "/sync") {
				operation = "sync"
			} else if r.Method != http.MethodPut && r.Method != http.MethodPatch {
				next.ServeHTTP(w, r)
				return
			}
			var fields map[string]json.RawMessage
			if operation == "update" {
				// Preserve native decoding and size rules for ordinary collections.
				probe := (listCollectionMutationGuard{pool: pool}).check(r.Context(), scope, claims.UserID, id, "probe", nil)
				if probe == nil {
					next.ServeHTTP(w, r)
					return
				}
				var authority *handlers.APIError
				if errors.As(probe, &authority) && authority.Status == 503 {
					http.Error(w, authority.Message, http.StatusServiceUnavailable)
					return
				}
				body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
				if err != nil || len(body) > 2<<20 {
					http.Error(w, "Invalid collection request", http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				if json.Unmarshal(body, &fields) != nil {
					http.Error(w, "Invalid collection request", http.StatusBadRequest)
					return
				}
			}
			if err := (listCollectionMutationGuard{pool: pool}).check(r.Context(), scope, claims.UserID, id, operation, fields); err != nil {
				var problem *handlers.APIError
				if !errors.As(err, &problem) {
					http.Error(w, "Collection authority unavailable", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(problem.Status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": problem.Message, "code": problem.Code})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type listGuardPersonalService struct {
	*handlers.CollectionHandler
	guard listCollectionMutationGuard
}

func (s *listGuardPersonalService) UpdatePersonalCollection(ctx context.Context, cmd handlers.PersonalCollectionUpdateCommand) (handlers.PersonalCollectionView, error) {
	r := cmd.Request
	fields := map[string]json.RawMessage{}
	for key, present := range map[string]bool{"source_url": r.SourceURL != nil, "max_items": r.MaxItems != nil, "library_ids": r.LibraryIDs != nil, "sync_schedule": r.SyncSchedule != nil, "query_definition": len(r.QueryDefinition) > 0, "display_query_definition": r.DisplayQueryDefinition != nil, "sort_config": len(r.SortConfig) > 0} {
		if present {
			fields[key] = nil
		}
	}
	if err := s.guard.check(ctx, "personal", cmd.UserID, cmd.CollectionID, "update", fields); err != nil {
		return handlers.PersonalCollectionView{}, err
	}
	return s.CollectionHandler.UpdatePersonalCollection(ctx, cmd)
}

type listGuardAdminService struct {
	*handlers.LibraryCollectionHandler
	guard listCollectionMutationGuard
}

func (s *listGuardAdminService) UpdateAdminCollection(ctx context.Context, id string, r handlers.AdminCollectionUpdate) (handlers.AdminCollection, error) {
	fields := map[string]json.RawMessage{}
	for key, present := range map[string]bool{"source_url": r.SourceURL != nil, "source_config": len(r.SourceConfig) > 0, "collection_type": r.CollectionType != nil, "library_ids": r.LibraryIDs != nil, "management_mode": r.ManagementMode != nil, "management_source": r.ManagementSource != nil, "management_key": r.ManagementKey != nil, "query_definition": len(r.QueryDefinition) > 0, "sort_config": len(r.SortConfig) > 0, "sync_schedule": r.SyncSchedule != nil} {
		if present {
			fields[key] = nil
		}
	}
	if err := s.guard.check(ctx, "server", 0, id, "update", fields); err != nil {
		return handlers.AdminCollection{}, err
	}
	return s.LibraryCollectionHandler.UpdateAdminCollection(ctx, id, r)
}
func (s *listGuardAdminService) AddAdminCollectionItem(ctx context.Context, id, item string, position int) error {
	if err := s.guard.check(ctx, "server", 0, id, "members", nil); err != nil {
		return err
	}
	return s.LibraryCollectionHandler.AddAdminCollectionItem(ctx, id, item, position)
}
func (s *listGuardAdminService) RemoveAdminCollectionItem(ctx context.Context, id, item string) error {
	if err := s.guard.check(ctx, "server", 0, id, "members", nil); err != nil {
		return err
	}
	return s.LibraryCollectionHandler.RemoveAdminCollectionItem(ctx, id, item)
}
func (s *listGuardAdminService) ReorderAdminCollectionItems(ctx context.Context, id string, items []string) error {
	if err := s.guard.check(ctx, "server", 0, id, "members", nil); err != nil {
		return err
	}
	return s.LibraryCollectionHandler.ReorderAdminCollectionItems(ctx, id, items)
}
func wireBloemListMutationGuards(deps *apiv2.Dependencies, pool *pgxpool.Pool) {
	if deps == nil || pool == nil {
		return
	}
	guard := listCollectionMutationGuard{pool: pool}
	if handler, ok := deps.PersonalCollections.(*handlers.CollectionHandler); ok {
		deps.PersonalCollections = &listGuardPersonalService{handler, guard}
	}
	if handler, ok := deps.AdminCollections.(*handlers.LibraryCollectionHandler); ok {
		deps.AdminCollections = &listGuardAdminService{handler, guard}
	}
}
