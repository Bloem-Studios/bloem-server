package managedtracking

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AuthorizeListTx fences the installation, grant, profile and exact enrollment
// through the caller's publication transaction. Native viewer/PIN policy is
// applied by the API adapter after this managed identity gate.
func (s *Service) AuthorizeListTx(ctx context.Context, tx pgx.Tx, installation int, instance, account, profile string) (Profile, error) {
	if s == nil || tx == nil || installation < 1 || instance == "" || !positiveID(account) || profile == "" {
		return Profile{}, ErrAuthority
	}
	info, err := authority(ctx, tx, installation, grantShare)
	if err != nil {
		return Profile{}, err
	}
	if info.InstanceID != instance {
		return Profile{}, ErrAuthority
	}
	// authority locks the grant; also fence plugin disable and organization
	// suspension, which are independent of the credential generation.
	var locked int
	err = tx.QueryRow(ctx, `SELECT i.id FROM plugin_installations i JOIN organizations o ON o.id=$2 WHERE i.id=$1 AND i.enabled AND (o.status='active' OR (o.is_default AND o.status='initializing')) FOR SHARE OF i,o`, installation, info.Scope).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAuthority
	}
	if err != nil {
		return Profile{}, err
	}
	// Native identity writers acquire these parents before updating the managed
	// projection. Keep that order and refuse background work for a PIN profile.
	err = tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1 AND p.id=$2 AND u.enabled AND COALESCE(p.pin_hash,'')='' AND p.organization_id=$3 FOR SHARE OF u,p`, account, profile, info.Scope).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAuthority
	}
	if err != nil {
		return Profile{}, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, account, profile)
	if err != nil {
		return Profile{}, err
	}
	var e Enrollment
	err = tx.QueryRow(ctx, `SELECT enrollment_id,generation,destination_account_id,destination_profile_id,active FROM bloem_managed_enrollments WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 FOR SHARE`, installation, account, profile).Scan(&e.ID, &e.Generation, &e.DestinationAccountID, &e.DestinationProfileID, &e.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAuthority
	}
	if err != nil {
		return Profile{}, err
	}
	e.Profile = p
	if !e.Active || e.Generation < info.minGeneration || e.Generation > info.Generation || e.ID != EnrollmentID(info.InstanceID, e) {
		return Profile{}, ErrAuthority
	}
	return p, nil
}
