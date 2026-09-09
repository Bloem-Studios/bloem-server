package access

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestGroupGuardAtomicLegacyAndGeneration(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	s, org := fixture.store, fixture.orgB
	defaultGroup := fixture.createDefaultGroup(org, "Default")
	g, err := s.Create(ctx, org, CreateGroupInput{Name: "Group"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for n := range 6 {
		wg.Go(func() {
			_, err := s.UpdateConditional(ctx, org, g.ID, UpdateGroupInput{Description: new(fmt.Sprintf("writer-%d", n))}, GroupPrecondition{Revision: g.Revision})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrGroupRevisionConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners=%d", wins)
	}
	current, err := s.Get(ctx, org, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Update(ctx, org, g.ID, UpdateGroupInput{DownloadAllowed: new(false), LibraryIDs: new([]int{})})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Revision <= current.Revision || legacy.LibraryIDs == nil || legacy.DownloadAllowed {
		t.Fatal("legacy writer lost revision/empty/false")
	}
	if err = s.DeleteConditional(ctx, org, g.ID, GroupPrecondition{Revision: current.Revision}); !errors.Is(err, ErrGroupRevisionConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	accountID := fixture.createUser(&g.ID, 1)
	profileID := fixture.createProfile(accountID, org, &g.ID)
	usage, err := s.Get(ctx, org, g.ID)
	if err != nil || usage.MemberCount != 1 || usage.Revision != legacy.Revision {
		t.Fatal("membership changed editor revision", err)
	}
	if err = s.DeleteConditional(ctx, org, g.ID, GroupPrecondition{Any: true}); err != nil {
		t.Fatal(err)
	}
	assertProfileAccessGroup(t, ctx, s.pool, accountID, profileID, defaultGroup.ID)
	var revision int64
	if err = s.pool.QueryRow(ctx, `INSERT INTO access_groups(id,organization_id,name) OVERRIDING SYSTEM VALUE VALUES($1,$2,'Recreated') RETURNING configuration_revision`, g.ID, org).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision <= usage.Revision {
		t.Fatal("recreated generation did not advance")
	}
	if err = s.DeleteConditional(ctx, org, g.ID, GroupPrecondition{Revision: usage.Revision}); !errors.Is(err, ErrGroupRevisionConflict) {
		t.Fatal("recreated row accepted stale guard", err)
	}
}
func TestGroupGuardDefaultAndPaging(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	s, org := fixture.store, fixture.orgB
	a, err := s.Create(ctx, org, CreateGroupInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, org, CreateGroupInput{Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConditional(ctx, org, a.ID, GroupPrecondition{Revision: a.Revision}); !errors.Is(err, ErrDefaultGroupRequired) {
		t.Fatal(err)
	}
	if _, err = s.UpdateConditional(ctx, org, a.ID, UpdateGroupInput{IsDefault: new(false)}, GroupPrecondition{Any: true}); !errors.Is(err, ErrDefaultGroupRequired) {
		t.Fatal(err)
	}
	promoted, err := s.UpdateConditional(ctx, org, b.ID, UpdateGroupInput{IsDefault: new(true)}, GroupPrecondition{Revision: b.Revision})
	if err != nil || !promoted.IsDefault {
		t.Fatal(err)
	}
	old, err := s.Get(ctx, org, a.ID)
	if err != nil || old.IsDefault || old.Revision <= a.Revision {
		t.Fatal("sibling demotion not revisioned", err)
	}
	if _, err = s.UpdateConditional(ctx, org, a.ID, UpdateGroupInput{IsDefault: new(true)}, GroupPrecondition{Revision: a.Revision}); !errors.Is(err, ErrGroupRevisionConflict) {
		t.Fatal("stale promotion allowed", err)
	}
	still, err := s.Get(ctx, org, b.ID)
	if err != nil || !still.IsDefault || still.Revision != promoted.Revision {
		t.Fatal("stale write modified default", err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO access_groups(organization_id,name) SELECT $1,'Paged-'||i FROM generate_series(1,205)i`, org); err != nil {
		t.Fatal(err)
	}
	page, more, err := s.ListPage(ctx, org, nil, 1000)
	if err != nil || len(page) != 200 || !more {
		t.Fatalf("page len=%d more=%v err=%v", len(page), more, err)
	}
	tail, more, err := s.ListPage(ctx, org, &GroupPageKey{ID: page[len(page)-1].ID}, 200)
	if err != nil || more || len(tail) != 7 {
		t.Fatalf("tail len=%d more=%v err=%v", len(tail), more, err)
	}
	for _, guard := range []GroupPrecondition{{}, {Revision: -1}, {Any: true, Revision: 1}} {
		if _, err = s.UpdateConditional(ctx, org, b.ID, UpdateGroupInput{}, guard); !errors.Is(err, ErrGroupInvalidPrecondition) {
			t.Fatal(err)
		}
	}
}

func TestGroupGuardPromotionRollbackAndConcurrentDefaults(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	s, org := fixture.store, fixture.orgB
	original, err := s.Create(ctx, org, CreateGroupInput{Name: "Original", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, org, CreateGroupInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	// A duplicate-name failure after clearing the sibling must roll that change back.
	_, err = s.UpdateConditional(ctx, org, other.ID, UpdateGroupInput{Name: new("Original"), IsDefault: new(true)}, GroupPrecondition{Revision: other.Revision})
	if !errors.Is(err, ErrGroupDuplicate) {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, org, original.ID)
	if err != nil || !current.IsDefault || current.Revision != original.Revision {
		t.Fatal("failed promotion changed sibling", err)
	}
	third, err := s.Create(ctx, org, CreateGroupInput{Name: "Third"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, g := range []*Group{other, third} {
		wg.Go(func() {
			_, err := s.UpdateConditional(ctx, org, g.ID, UpdateGroupInput{IsDefault: new(true)}, GroupPrecondition{Revision: g.Revision})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	// Both different-row edits may serialize successfully, but exactly one default
	// remains and the first winner's subsequent demotion advances its revision.
	for err := range results {
		if err != nil && !errors.Is(err, ErrGroupRevisionConflict) {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1 AND is_default`, org).Scan(&count); err != nil || count != 1 {
		t.Fatal("default invariant", count, err)
	}
}
