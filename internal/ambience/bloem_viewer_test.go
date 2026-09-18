package ambience

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/sections/recipes"
)

func TestBloemSeasonalViewerFiltersCurrentTenantAndActiveMembership(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	account := seedAccount(t, pool, "seasonal-viewer")
	stranger := seedAccount(t, pool, "seasonal-stranger")
	current := seedOrganization(t, pool, "seasonal-current", account, account)
	other := seedOrganization(t, pool, "seasonal-other", account, account)
	foreign := seedOrganization(t, pool, "seasonal-foreign", stranger, stranger)
	svc := NewService(pool, recipes.FixedClock(winterStart), nil)
	for _, in := range []Input{winterInput("public", nil), winterInput("current", &current), winterInput("other", &other), winterInput("foreign", &foreign)} {
		if _, err := svc.Create(ctx, account, in); err != nil {
			t.Fatal(err)
		}
	}
	check := func(org uuid.UUID, want ...string) {
		t.Helper()
		packs, err := svc.ActiveForBloemViewer(ctx, account, org)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(packs))
		for _, pack := range packs {
			got = append(got, pack.EffectID)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("effects = %v, want %v", got, want)
		}
	}
	check(current, "public", "current")
	check(other, "public", "other")
	check(foreign, "public")
	// The native adapter must not broaden the public branding contract.
	public, err := svc.ActivePublic(ctx)
	if err != nil || len(public) != 1 || public[0].EffectID != "public" {
		t.Fatalf("public packs = %+v, error = %v", public, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships SET status='suspended' WHERE organization_id=$1 AND account_id=$2`, current, account); err != nil {
		t.Fatal(err)
	}
	check(current, "public")
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	check(other, "public")
}

func TestBloemSeasonalViewerRejectsMissingSubject(t *testing.T) {
	svc := NewService(nil, nil, nil)
	for _, subject := range []struct {
		account int
		org     uuid.UUID
	}{{0, uuid.New()}, {1, uuid.Nil}} {
		if _, err := svc.ActiveForBloemViewer(t.Context(), subject.account, subject.org); err == nil {
			t.Fatal("missing viewer subject was accepted")
		}
	}
}

// Retarget immediately after the first read, before an adapter can issue a
// second authorization query. Content and its organization must share a snapshot.
func TestBloemSeasonalViewerRetargetDoesNotExposePreviousOrganization(t *testing.T) {
	pool := newMigratedTestPool(t)
	account := seedAccount(t, pool, "retarget-viewer")
	current := seedOrganization(t, pool, "retarget-current", account, account)
	other := seedOrganization(t, pool, "retarget-other", account, account)
	svc := NewService(pool, recipes.FixedClock(winterStart), nil)
	pack, err := svc.Create(t.Context(), account, winterInput("other-private-content", &other))
	if err != nil {
		t.Fatal(err)
	}
	trace := &bloemRetargetTrace{retarget: func() {
		if _, err := svc.Update(t.Context(), pack.ID, winterInput("current-content", &current)); err != nil {
			t.Fatal(err)
		}
	}}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	viewerPool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer viewerPool.Close()
	viewer := NewService(viewerPool, recipes.FixedClock(winterStart), nil)
	packs, err := viewer.ActiveForBloemViewer(t.Context(), account, current)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.fired {
		t.Fatal("retarget did not run at the read boundary")
	}
	for _, got := range packs {
		if got.EffectID == "other-private-content" {
			t.Fatal("returned previous organization's content after retarget")
		}
	}
	packs, err = viewer.ActiveForBloemViewer(t.Context(), account, current)
	if err != nil || len(packs) != 1 || packs[0].EffectID != "current-content" {
		t.Fatalf("retargeted readback = %+v, error = %v", packs, err)
	}
}

type bloemRetargetTrace struct {
	fired    bool
	retarget func()
}

type bloemRetargetQueryKey struct{}

func (t *bloemRetargetTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, bloemRetargetQueryKey{}, strings.Contains(data.SQL, "FROM ambience_packs"))
}

func (t *bloemRetargetTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if selected, _ := ctx.Value(bloemRetargetQueryKey{}).(bool); selected && !t.fired {
		t.fired = true
		t.retarget()
	}
}
