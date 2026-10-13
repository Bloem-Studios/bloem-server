package managedtracking

import (
	"errors"
	"testing"
)

func TestListAuthorityRequiresCurrentEnrollment(t *testing.T) {
	s, installation, profile := enrollmentFixture(t)
	ctx := t.Context()
	info, err := s.GetInfo(ctx, installation)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		got, err := s.AuthorizeListTx(ctx, tx, installation, info.InstanceID, profile.AccountID, profile.ProfileID)
		if want && (err != nil || got.ProfileID != profile.ProfileID) {
			t.Fatalf("authorized profile: %v %v", got, err)
		}
		if !want && !errors.Is(err, ErrAuthority) {
			t.Fatalf("unauthorized enrollment: %v", err)
		}
	}
	check(false)
	e := Enrollment{Profile: profile, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err = s.EnsureConnection(ctx, installation, e); err != nil {
		t.Fatal(err)
	}
	check(true)
	if _, err = s.Pool.Exec(ctx, `UPDATE user_profiles SET name='Changed after enrollment' WHERE id=$1`, profile.ProfileID); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestListAuthorityRejectsDisabledInstallationAndRotatedGrant(t *testing.T) {
	for _, change := range []string{"installation", "rotation", "enrollment", "instance", "pin", "account"} {
		t.Run(change, func(t *testing.T) {
			s, installation, profile := enrollmentFixture(t)
			ctx := t.Context()
			info, err := s.GetInfo(ctx, installation)
			if err != nil {
				t.Fatal(err)
			}
			e := Enrollment{Profile: profile, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
			e.ID = EnrollmentID(info.InstanceID, e)
			if err = s.EnsureConnection(ctx, installation, e); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "installation":
				_, err = s.Pool.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, installation)
			case "rotation":
				err = s.Rotate(ctx, installation)
			case "enrollment":
				_, err = s.Pool.Exec(ctx, `UPDATE bloem_managed_enrollments SET active=false WHERE installation_id=$1`, installation)
			case "instance":
				info.InstanceID = "wrong-instance"
			case "pin":
				_, err = s.Pool.Exec(ctx, `UPDATE user_profiles SET pin_hash='synthetic-pin' WHERE id=$1`, profile.ProfileID)
			case "account":
				_, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, profile.AccountID)
			}
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = s.AuthorizeListTx(ctx, tx, installation, info.InstanceID, profile.AccountID, profile.ProfileID); !errors.Is(err, ErrAuthority) {
				t.Fatalf("change accepted: %v", err)
			}
		})
	}
}
