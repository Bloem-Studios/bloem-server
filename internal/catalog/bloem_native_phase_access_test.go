package catalog

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
)

// Query denial double only: proves the owning host predicate uses the supplied
// query, not the repository pool. Actual uncommitted skeleton visibility is an
// authenticated integration gate after root composes the candidate.
type nativePhaseDeniedQuery struct {
	calls int
	sql   string
	args  []any
}

func (q *nativePhaseDeniedQuery) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected list query")
}
func (q *nativePhaseDeniedQuery) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.calls++
	q.sql = sql
	q.args = args
	return nativePhaseDeniedRow{}
}

type nativePhaseDeniedRow struct{}

func (nativePhaseDeniedRow) Scan(...any) error { return pgx.ErrNoRows }
func TestNativePhaseHostPredicateUsesSuppliedQuery(t *testing.T) {
	q := &nativePhaseDeniedQuery{}
	filter := AccessFilter{AllowedLibraryIDs: []int{2}, DisabledLibraryIDs: []int{3}}
	err := (NativePhaseItemAccess{Query: q}).EnsureAccessible(t.Context(), "target", filter)
	if !errors.Is(err, ErrItemNotFound) || q.calls != 1 {
		t.Fatalf("supplied query was not used: %v %d", err, q.calls)
	}
	if len(q.args) < 3 {
		t.Fatal("library allow/deny predicate lost")
	}
	q.calls = 0
	err = (NativePhaseItemAccess{Query: q}).EnsureAccessible(t.Context(), "target", AccessFilter{AllowedLibraryIDs: []int{}})
	if !errors.Is(err, ErrItemNotFound) || q.calls != 0 {
		t.Fatal("explicit empty scope bypassed")
	}
}
