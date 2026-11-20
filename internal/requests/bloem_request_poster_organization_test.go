package requests

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// An administrator reads request posters of their own organization only, as
// with every other admin request read.
func TestBloemAdminRequestPosterStaysInItsOrganization(t *testing.T) {
	const requester, adminT, adminE = 7, 1, 2
	base := newFakeStore()
	seedRequest(base, "req", requester)
	store := &stampedStore{fakeStore: base, orgOf: map[string]uuid.UUID{"req": orgBeta}}
	svc := NewService(store, &fakeTMDBClient{}, &fakePresence{})
	svc.SetUserRepository(requestUserRepo{})
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{requester: orgBeta, adminT: orgAlpha, adminE: orgBeta}})

	if err := svc.BloemRequireAdminRequestOrganization(actingIn(adminT, orgAlpha), adminViewer(adminT), "req"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("T admin reading an E request poster = %v, want ErrForbidden", err)
	}
	if err := svc.BloemRequireAdminRequestOrganization(actingIn(adminE, orgBeta), adminViewer(adminE), "req"); err != nil {
		t.Fatalf("E admin reading an E request poster: %v", err)
	}
	if err := svc.BloemRequireAdminRequestOrganization(actingIn(adminE, orgBeta), adminViewer(adminE), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("poster of a missing request = %v, want ErrNotFound", err)
	}
	// Without organizations the check passes and Silo's read decides.
	plain := NewService(newFakeStore(), &fakeTMDBClient{}, &fakePresence{})
	if err := plain.BloemRequireAdminRequestOrganization(actingIn(adminT, orgAlpha), adminViewer(adminT), "missing"); err != nil {
		t.Fatalf("no tenant scope: %v", err)
	}
}
