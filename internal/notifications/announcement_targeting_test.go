package notifications

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

type fakeRecipientSource struct {
	users    []*models.User
	profiles map[int][]userstore.Profile
	orgs     map[uuid.UUID][]int
	allowed  map[string][]int // profileID -> allowed library ids; absent = unrestricted
}

func (f *fakeRecipientSource) ListUsers(context.Context) ([]*models.User, error) { return f.users, nil }
func (f *fakeRecipientSource) ListProfiles(_ context.Context, userID int) ([]userstore.Profile, error) {
	return f.profiles[userID], nil
}
func (f *fakeRecipientSource) OrganizationAccountIDs(_ context.Context, id uuid.UUID) ([]int, error) {
	return f.orgs[id], nil
}
func (f *fakeRecipientSource) ProfileLibraryAllowed(_ context.Context, _ int, profileID string, libraryID int) (bool, error) {
	ids, restricted := f.allowed[profileID]
	if !restricted {
		return true, nil
	}
	for _, id := range ids {
		if id == libraryID {
			return true, nil
		}
	}
	return false, nil
}

func targetingFixture() (*fakeRecipientSource, uuid.UUID) {
	org := uuid.New()
	return &fakeRecipientSource{
		users: []*models.User{
			{ID: 1, Role: models.RoleAdmin, Enabled: true},
			{ID: 2, Role: models.RoleUser, Enabled: true},
			{ID: 3, Role: models.RoleUser, Enabled: false},
		},
		profiles: map[int][]userstore.Profile{
			1: {{ID: "p1a"}, {ID: "p1b"}},
			2: {{ID: "p2a"}},
			3: {{ID: "p3a"}},
		},
		orgs:    map[uuid.UUID][]int{org: {2, 3}},
		allowed: map[string][]int{"p1b": {9}, "p2a": {5}},
	}, org
}

func recipientProfiles(t *testing.T, src recipientSource, targeting AnnouncementTargeting) []string {
	t.Helper()
	validated, err := validateTargeting(targeting)
	if err != nil {
		t.Fatalf("validate %+v: %v", targeting, err)
	}
	got, err := resolveAnnouncementRecipients(context.Background(), src, validated)
	if err != nil {
		t.Fatalf("resolve %+v: %v", targeting, err)
	}
	ids := make([]string, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.ProfileID)
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveAnnouncementRecipients(t *testing.T) {
	src, org := targetingFixture()
	cases := []struct {
		name      string
		targeting AnnouncementTargeting
		want      []string
	}{
		{"all skips disabled accounts", AnnouncementTargeting{}, []string{"p1a", "p1b", "p2a"}},
		{"role admin", AnnouncementTargeting{Audience: "Role", Role: "ADMIN"}, []string{"p1a", "p1b"}},
		{"organization active members only", AnnouncementTargeting{Audience: AudienceOrganization, OrganizationID: org.String()}, []string{"p2a"}},
		{"library entitled", AnnouncementTargeting{Audience: AudienceLibrary, LibraryID: 5}, []string{"p1a", "p2a"}},
		{"explicit users and profiles dedupe", AnnouncementTargeting{Audience: AudienceExplicit, UserIDs: []int{2, 3}, ProfileIDs: []string{"p1b", "p2a", "p3a", "nope"}}, []string{"p2a", "p1b"}},
	}
	for _, tc := range cases {
		if got := recipientProfiles(t, src, tc.targeting); !equalStrings(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateTargetingRejectsBadInput(t *testing.T) {
	cases := []AnnouncementTargeting{
		{Audience: "everyone"},
		{Audience: AudienceRole, Role: "owner"},
		{Audience: AudienceOrganization, OrganizationID: "not-a-uuid"},
		{Audience: AudienceLibrary},
		{Audience: AudienceExplicit},
	}
	for _, in := range cases {
		if _, err := validateTargeting(in); !errors.Is(err, ErrAnnouncementInvalid) {
			t.Errorf("%+v: expected ErrAnnouncementInvalid, got %v", in, err)
		}
	}
	// Side fields outside the audience are dropped rather than stored.
	got, err := validateTargeting(AnnouncementTargeting{Audience: AudienceAll, Role: "admin", LibraryID: 3, UserIDs: []int{1}})
	if err != nil || got.Role != "" || got.LibraryID != 0 || got.UserIDs != nil {
		t.Fatalf("stray fields kept: %+v (%v)", got, err)
	}
}
