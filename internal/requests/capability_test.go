package requests

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func TestRequestCapabilityAllowedUsesAccountAndGroupPolicy(t *testing.T) {
	for _, tc := range []struct {
		name         string
		groupAllowed bool
		override     *bool
		limit        *UserLimit
		want         bool
	}{
		{name: "group denied", groupAllowed: false, want: false},
		{name: "account overrides group", groupAllowed: false, override: new(true), want: true},
		{name: "account denied", groupAllowed: true, override: new(false), want: false},
		{name: "blocked limit", groupAllowed: true, limit: &UserLimit{LimitMode: LimitModeBlocked}, want: false},
		{name: "blocked approval", groupAllowed: true, limit: &UserLimit{ApprovalMode: ApprovalModeBlocked}, want: false},
		{name: "exhausted quota is capacity", groupAllowed: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.limit = tc.limit
			store.count = 100
			store.settings.GlobalMaxRequests = 1
			service := newTestService(store)
			groupID := int64(1)
			service.SetUserRepository(requestUserRepo{user: &models.User{ID: 1, AccessGroupID: &groupID, RequestsAllowed: tc.override}})
			service.SetGroupPolicyProvider(requestGroupProvider{group: &access.GroupPolicy{RequestsAllowed: tc.groupAllowed}})
			// ensureViewerRequestsAllowed consults the group provider only when a
			// group subject resolves, and GroupSubjectFromContext requires a tenant
			// whose AccountID matches the viewer. Production reaches this through
			// the v2 request-lifecycle route, which runs the tenancy middleware, so
			// the test has to model that context or it silently exercises the
			// no-group fallback instead of the group policy it is asserting on.
			ctx := tenancy.WithContext(t.Context(), tenancy.Context{
				OrganizationID:     uuid.New(),
				MembershipID:       uuid.New(),
				AccountID:          1,
				OrganizationStatus: tenancy.OrganizationActive,
				MembershipStatus:   tenancy.MembershipActive,
				PolicyRevision:     1,
				SecurityRevision:   1,
			})
			got, err := service.RequestCapabilityAllowed(ctx, testViewer(1))
			if err != nil || got != tc.want {
				t.Fatalf("allowed=%v err=%v want %v", got, err, tc.want)
			}
		})
	}
}
