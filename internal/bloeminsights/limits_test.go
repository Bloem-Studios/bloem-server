package bloeminsights

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestComputeUsage(t *testing.T) {
	sessions := []LiveSession{
		{UserID: 7, Username: "anna", EffectivePlayMethod: "transcode"},
		{UserID: 7, Username: "anna", EffectivePlayMethod: "direct"},
		{UserID: 8, Username: "ben", EffectivePlayMethod: "transcode"},
		{UserID: 9, Username: "cas", EffectivePlayMethod: "direct"},
	}
	limits := func(_ context.Context, userID int) (playback.SessionLimits, error) {
		switch userID {
		case 7:
			return playback.SessionLimits{MaxStreams: 2, MaxTranscodes: 1, TenantID: "org-1", TenantMaxTranscodes: 4}, nil
		case 8, 9:
			return playback.SessionLimits{MaxStreams: 3, Household: playback.HouseholdLimits{ID: 9, MaxStreams: 4, MaxTranscodes: 2}, TenantID: "org-1", TenantMaxTranscodes: 4}, nil
		}
		return playback.SessionLimits{}, nil
	}
	people, err := ComputeUsage(context.Background(), sessions, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 3 {
		t.Fatalf("people = %+v, want 3", people)
	}
	anna := people[0]
	if anna.UserID != 7 || anna.Username != "anna" || anna.Streams != (Count{Active: 2, Max: 2}) || anna.Transcodes != (Count{Active: 1, Max: 1}) {
		t.Fatalf("anna = %+v", anna)
	}
	if anna.Household != nil {
		t.Fatalf("anna has a household pool: %+v", anna.Household)
	}
	if anna.Organization == nil || anna.Organization.Transcodes == nil || *anna.Organization.Transcodes != (Count{Active: 2, Max: 4}) || anna.Organization.Streams != nil {
		t.Fatalf("anna's organization pool = %+v, want 2 of 4 transcodes", anna.Organization)
	}
	ben, cas := people[1], people[2]
	if ben.Household == nil || cas.Household == nil || ben.Household.ID != "9" || cas.Household.ID != "9" {
		t.Fatalf("household pools = %+v / %+v, want both in 9", ben.Household, cas.Household)
	}
	if *ben.Household.Streams != (Count{Active: 2, Max: 4}) || *ben.Household.Transcodes != (Count{Active: 1, Max: 2}) {
		t.Fatalf("household 9 = streams %+v transcodes %+v, want 2/4 and 1/2", *ben.Household.Streams, *ben.Household.Transcodes)
	}
	if *cas.Household.Streams != *ben.Household.Streams {
		t.Fatal("two members of one household see different pools")
	}
	if cas.Streams != (Count{Active: 1, Max: 3}) || cas.Transcodes != (Count{Active: 0, Max: 0}) {
		t.Fatalf("cas = %+v", cas)
	}
}
