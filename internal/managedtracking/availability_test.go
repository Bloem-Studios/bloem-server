package managedtracking

import (
	"context"
	"errors"
	"testing"
	"time"

	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
)

type availabilityFixtureSource struct {
	calendarFixtureSource
	afterLookup func()
}

func (f *availabilityFixtureSource) LookupAvailability(_ context.Context, _ Profile, targets []*managedv1.AvailabilityTarget) ([]*managedv1.AvailabilityResult, error) {
	if f.afterLookup != nil {
		f.afterLookup()
	}
	out := []*managedv1.AvailabilityResult{}
	for _, target := range targets {
		out = append(out, &managedv1.AvailabilityResult{RequestId: target.RequestId, Available: true, ContentId: "synthetic-item"})
	}
	return out, nil
}
func TestAvailabilityRechecksAuthorityAndBounds(t *testing.T) {
	f := enrolledFixture(t)
	src := &availabilityFixtureSource{calendarFixtureSource: calendarFixtureSource{digest: "scope"}}
	f.s.Calendar = src
	req := &managedv1.AvailabilityRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Targets: []*managedv1.AvailabilityTarget{{RequestId: "request", Kind: "movie", Namespace: "tmdb", ExternalId: "123"}}}
	out, err := f.s.LookupAvailability(t.Context(), f.id, req)
	if err != nil || len(out.Results) != 1 || !out.Results[0].Available || out.ExpiresAtUnix-out.CheckedAtUnix != 120 {
		t.Fatalf("lookup/lease: %v %v", out, err)
	}
	req.Targets = append(req.Targets, req.Targets[0])
	if _, err = f.s.LookupAvailability(t.Context(), f.id, req); !errors.Is(err, ErrSnapshot) {
		t.Fatalf("duplicate accepted: %v", err)
	}
	req.Targets = req.Targets[:1]
	src.afterLookup = func() { src.digest = "changed" }
	if _, err = f.s.LookupAvailability(t.Context(), f.id, req); !errors.Is(err, ErrAuthority) {
		t.Fatalf("authority race exported: %v", err)
	}
	src.digest = "scope"
	src.afterLookup = func() {
		_, e := f.s.Pool.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, f.id)
		if e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
	defer cancel()
	if _, err = f.s.LookupAvailability(ctx, f.id, req); !errors.Is(err, ErrAuthority) {
		t.Fatalf("grant race exported: %v", err)
	}
}
