package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/go-chi/chi/v5"
)

// Raw stores in the generic admin handler bypass Service isolation. Block all
// ID-based dispatch for marked native installations, including config testing.
func guardNativeStorageInstallation(registry plugins.NativeStorageIsolationRegistry, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil || id <= 0 {
			http.Error(w, "invalid installation", http.StatusBadRequest)
			return
		}
		if registry == nil {
			http.Error(w, "native storage isolation unavailable", http.StatusServiceUnavailable)
			return
		}
		marked, err := registry.NativeStorageIDs(r.Context(), []int{id})
		if err != nil {
			http.Error(w, "native storage isolation unavailable", http.StatusServiceUnavailable)
			return
		}
		if marked[id] {
			http.Error(w, "native storage requires authorized source lifecycle", http.StatusConflict)
			return
		}
		next(w, r)
	}
}
func nativeStorageRegistry(deps Dependencies) plugins.NativeStorageIsolationRegistry {
	if deps.NativeStorage != nil {
		return deps.NativeStorage.Registry
	}
	if deps.DB != nil {
		return nativeStorageMarkers{pool: deps.DB}
	}
	return nil
}

// The production router and tests share this registration so none of the raw
// installation stores can accidentally be mounted without isolation.
type nativeStorageAdminDispatch struct {
	Update, ApplyUpdate, TestConfig, PutConfig, PutAuthBinding, PutTaskBinding, Delete http.HandlerFunc
}

func registerNativeStorageInstallationRoutes(r chi.Router, registry plugins.NativeStorageIsolationRegistry, h nativeStorageAdminDispatch) {
	r.Put("/installations/{id}", guardNativeStorageInstallation(registry, h.Update))
	r.Post("/installations/{id}/update", guardNativeStorageInstallation(registry, h.ApplyUpdate))
	r.Post("/installations/{id}/config/test", guardNativeStorageInstallation(registry, h.TestConfig))
	r.Put("/installations/{id}/config", guardNativeStorageInstallation(registry, h.PutConfig))
	r.Put("/installations/{id}/auth-binding", guardNativeStorageInstallation(registry, h.PutAuthBinding))
	r.Put("/installations/{id}/task-bindings/{capability_id}", guardNativeStorageInstallation(registry, h.PutTaskBinding))
	r.Delete("/installations/{id}", guardNativeStorageInstallation(registry, h.Delete))
}

// Embedded routers without a native runtime still consult durable markers;
// ordinary plugin administration does not require enabling native readers.
type nativeStorageMarkers struct{ pool *pgxpool.Pool }

func (s nativeStorageMarkers) NativeStorageIDs(ctx context.Context, ids []int) (map[int]bool, error) {
	result := make(map[int]bool)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT installation_id FROM bloem_storage_installations WHERE installation_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
