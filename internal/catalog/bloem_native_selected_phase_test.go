package catalog

import (
	"context"
	"github.com/jackc/pgx/v5"
	"testing"
)

type forbiddenPhaseQuery struct{ t *testing.T }

func (q forbiddenPhaseQuery) Query(context.Context, string, ...any) (pgx.Rows, error) {
	q.t.Fatal("query before auth refusal")
	return nil, nil
}
func (q forbiddenPhaseQuery) QueryRow(context.Context, string, ...any) pgx.Row {
	q.t.Fatal("classification before auth refusal")
	return nil
}
func TestNativePhaseOriginCannotBeReplacedOrLostOnDetach(t *testing.T) {
	calls := 0
	q := forbiddenPhaseQuery{t}
	origin := WithNativePhaseAuthorizer(t.Context(), q, func(context.Context, NativePhaseQuery, NativePhaseTargets) error {
		calls++
		return &NativePhaseRefusal{Code: "not_found"}
	})
	nested := WithNativePhaseAuthorizer(origin, q, func(context.Context, NativePhaseQuery, NativePhaseTargets) error {
		t.Fatal("origin replaced")
		return nil
	})
	detached := CarryNativePhaseOrigin(nested, context.Background())
	if err := RequireNativePhase(detached, nil, NativePhaseTargets{ContentIDs: []string{"hidden"}}); !IsNativePhaseRefusal(err) {
		t.Fatalf("refusal=%v", err)
	}
	if calls != 1 {
		t.Fatalf("origin calls=%d", calls)
	}
	if err := RequireNativePhase(context.Background(), q, NativePhaseTargets{ContentIDs: []string{"native enrichment"}}); err != nil {
		t.Fatal(err)
	}
	marked := WithNativePhaseAuthorizer(t.Context(), q, nil)
	if err := RequireNativePhase(marked, nil, NativePhaseTargets{}); !IsNativePhaseRefusal(err) {
		t.Fatal("marked request became trusted")
	}
}
