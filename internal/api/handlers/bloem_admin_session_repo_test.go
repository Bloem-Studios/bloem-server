package handlers

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// TestBloemAdminSessionRepoUsesTheHandlersLoginSessions: the account session
// routes read the session repository Silo's NewAdminHandler builds from the
// pool, so they work without Bloem setting a repository of its own.
func TestBloemAdminSessionRepoUsesTheHandlersLoginSessions(t *testing.T) {
	// pgxpool.New does not connect until a query runs.
	pool, err := pgxpool.New(context.Background(), "postgres://bloem@127.0.0.1:1/bloem")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	h := NewAdminHandler(nil, pool, nil)
	if _, ok := h.adminSessionRepo().(*auth.SessionRepository); !ok {
		t.Fatalf("adminSessionRepo() = %T, want *auth.SessionRepository", h.adminSessionRepo())
	}
	if (&AdminHandler{}).adminSessionRepo() != nil {
		t.Fatal("a handler without a pool has no session repository")
	}
}
