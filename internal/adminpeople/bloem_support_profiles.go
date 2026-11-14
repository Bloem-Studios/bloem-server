package adminpeople

// Bloem-owned. Profile edits an organization administrator may make for a
// member: rename, and clear a forgotten PIN. Adding and removing a profile run
// through Silo's profile lifecycle and live in the API handlers; they use the
// same authority rules, audit events and budgets defined here.
//
// An administrator never sets a PIN. "Reset" clears it, and the member (or the
// household's main profile) chooses a new one, so a PIN is never known to a
// third party. Parental fields are out of scope: they belong to the household
// and to access groups.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxProfileNameLength bounds a profile name an administrator may set.
const MaxProfileNameLength = 100

var (
	// ErrInvalidProfileName is a name that is empty, too long or has control characters.
	ErrInvalidProfileName = errors.New("invalid profile name")
	// ErrProfileNameConflict is a name another profile of the same account already uses.
	ErrProfileNameConflict = errors.New("profile name already in use")
	// ErrPrimaryProfileProtected refuses removing the household's primary profile.
	ErrPrimaryProfileProtected = errors.New("the primary profile cannot be removed")
)

// ProfileRevisionError is a stale profile revision; Current is the value now.
type ProfileRevisionError struct{ Current int64 }

func (e *ProfileRevisionError) Error() string { return "profile revision changed" }

// ValidateProfileName trims and checks a profile name.
func ValidateProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxProfileNameLength {
		return "", ErrInvalidProfileName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidProfileName
		}
	}
	return name, nil
}

// SupportProfile is the profile and the person after a profile write.
type SupportProfile struct {
	Profile ProfileSummary
	Person  PersonSummary
}

// LockedProfile is a member's profile of this organization, read under lock.
type LockedProfile struct {
	ProfileSummary
}

// LockProfile reads and locks the member's profile of this organization after
// the membership and account (the order People writes take them in). A profile
// that is not the member's, or not this organization's, is ErrNotFound.
func LockProfile(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, profileID string) (LockedProfile, error) {
	var profile LockedProfile
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.name,p.access_group_id,g.name,p.updated_at,p.revision,p.pin_hash<>'',p.is_primary
		FROM user_profiles p JOIN access_groups g ON g.organization_id=p.organization_id AND g.id=p.access_group_id
		WHERE p.organization_id=$1 AND p.user_id=$2 AND p.id=$3 FOR UPDATE OF p`, organizationID, accountID, profileID).
		Scan(&profile.ID, &profile.Name, &profile.GroupID, &profile.GroupName, &profile.UpdatedAt, &profile.Revision, &profile.HasPIN, &profile.IsPrimary)
	if errors.Is(err, pgx.ErrNoRows) {
		return profile, ErrNotFound
	}
	return profile, err
}

func profileEvent(actor MutationActor, organizationID uuid.UUID, accountID int, action, profileID string, locked LockedAuthority, profile LockedProfile) AuditEvent {
	event := supportEvent(actor, organizationID, accountID, action, "profile", profileID, locked)
	event.BeforeRevision, event.AfterRevision = profile.Revision, profile.Revision
	return event
}

// supportProfileTx is the prologue of a profile write: budget, authority, the
// profile lock and its revision check.
func supportProfileTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor, accountID int, profileID string, expectedRevision int64, refusedAction string) (LockedAuthority, LockedProfile, error) {
	if err := LockBudget(ctx, tx, BudgetMutate, organizationID); err != nil {
		return LockedAuthority{}, LockedProfile{}, err
	}
	if err := CheckBudget(ctx, tx, BudgetMutate, organizationID, actor.AccountID, strconv.Itoa(accountID)); err != nil {
		return LockedAuthority{}, LockedProfile{}, err
	}
	locked, err := supportAuthority(ctx, tx, organizationID, actor, accountID, refusedAction, "profile", profileID)
	if err != nil {
		return locked, LockedProfile{}, err
	}
	profile, err := LockProfile(ctx, tx, organizationID, accountID, profileID)
	if err != nil {
		return locked, profile, err
	}
	if profile.Revision != expectedRevision {
		return locked, profile, &ProfileRevisionError{Current: profile.Revision}
	}
	return locked, profile, nil
}

// RenameProfile renames a member's profile. The name is unique per account,
// case-insensitively. It raises the profile's revision, not the person's
// security revision, so the member stays signed in.
func (s *Service) RenameProfile(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, profileID, name string, expectedRevision int64) (SupportProfile, error) {
	name, err := ValidateProfileName(name)
	if err != nil {
		return SupportProfile{}, err
	}
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (SupportProfile, error) {
		var zero SupportProfile
		locked, profile, err := supportProfileTx(ctx, tx, organizationID, actor, accountID, profileID, expectedRevision, "people.profile_rename_refused")
		if err != nil {
			return zero, err
		}
		var conflict bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_profiles WHERE user_id = $1 AND id <> $2 AND lower(btrim(name)) = lower($3))`, accountID, profileID, name).Scan(&conflict); err != nil {
			return zero, err
		}
		if conflict {
			return zero, RefuseAudited(ctx, tx, profileEvent(actor, organizationID, accountID, "people.profile_rename_refused", profileID, locked, profile), "name_conflict", ErrProfileNameConflict)
		}
		if _, err := tx.Exec(ctx, `UPDATE user_profiles SET name = $4, updated_at = now() WHERE organization_id = $1 AND user_id = $2 AND id = $3`, organizationID, accountID, profileID, name); err != nil {
			return zero, fmt.Errorf("rename profile: %w", err)
		}
		event := profileEvent(actor, organizationID, accountID, "people.profile_renamed", profileID, locked, profile)
		event.AfterRevision = profile.Revision + 1
		event.Before = map[string]any{"name": profile.Name}
		event.After = map[string]any{"name": name}
		if err := RecordAudit(ctx, tx, event); err != nil {
			return zero, err
		}
		return supportProfileResult(ctx, tx, organizationID, accountID, profileID)
	})
}

// ClearProfilePIN removes a member's profile PIN, so the member or the
// household's main profile can choose a new one. Clearing the primary
// profile's PIN is allowed (it fixes the common "the parent forgot" case) and
// is audited like any other. Clearing a profile that has no PIN succeeds.
// The wrong-PIN attempt counter lives outside the database: the caller resets
// it after commit.
func (s *Service) ClearProfilePIN(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, profileID string, expectedRevision int64) (SupportProfile, error) {
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (SupportProfile, error) {
		var zero SupportProfile
		locked, profile, err := supportProfileTx(ctx, tx, organizationID, actor, accountID, profileID, expectedRevision, "people.profile_pin_clear_refused")
		if err != nil {
			return zero, err
		}
		if profile.HasPIN {
			if _, err := tx.Exec(ctx, `UPDATE user_profiles SET pin_hash = '', updated_at = now() WHERE organization_id = $1 AND user_id = $2 AND id = $3`, organizationID, accountID, profileID); err != nil {
				return zero, fmt.Errorf("clear profile pin: %w", err)
			}
		}
		event := profileEvent(actor, organizationID, accountID, "people.profile_pin_cleared", profileID, locked, profile)
		if profile.HasPIN {
			event.AfterRevision = profile.Revision + 1
		}
		event.After = map[string]any{"had_pin": profile.HasPIN, "primary": profile.IsPrimary}
		if err := RecordAudit(ctx, tx, event); err != nil {
			return zero, err
		}
		return supportProfileResult(ctx, tx, organizationID, accountID, profileID)
	})
}

func supportProfileResult(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, profileID string) (SupportProfile, error) {
	person, err := getPersonInTransaction(ctx, tx, organizationID, accountID)
	if err != nil {
		return SupportProfile{}, err
	}
	for _, profile := range person.Profiles {
		if profile.ID == profileID {
			return SupportProfile{Profile: profile, Person: person}, nil
		}
	}
	return SupportProfile{}, ErrNotFound
}

// ProfileRemovalPlan is a removal that passed its checks and is ready to run.
type ProfileRemovalPlan struct {
	Authority LockedAuthority
	Profile   LockedProfile
}

// PlanProfileRemoval judges a profile removal: budget, authority, the primary
// profile rule and the profile revision, in one transaction whose refusals are
// audited. It changes nothing. Removing a profile deletes its watch history
// and personal collections and cannot be undone; Silo's lifecycle does the
// delete in its own transaction, which re-judges authority through
// FinishProfileRemoval before it commits.
func (s *Service) PlanProfileRemoval(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, profileID string, expectedRevision int64) (ProfileRemovalPlan, error) {
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (ProfileRemovalPlan, error) {
		var zero ProfileRemovalPlan
		locked, profile, err := supportProfileTx(ctx, tx, organizationID, actor, accountID, profileID, expectedRevision, "people.profile_remove_refused")
		if err != nil {
			return zero, err
		}
		if profile.IsPrimary {
			return zero, RefuseAudited(ctx, tx, profileEvent(actor, organizationID, accountID, "people.profile_remove_refused", profileID, locked, profile), "primary_profile_protected", ErrPrimaryProfileProtected)
		}
		return ProfileRemovalPlan{Authority: locked, Profile: profile}, nil
	})
}

// FinishProfileRemoval runs inside Silo's profile-delete transaction, before it
// commits. It re-judges the actor's authority over the member under lock, and
// that the profile is still the one that was planned (same name), then records the removal. Any error aborts the delete.
func FinishProfileRemoval(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor, accountID int, plan ProfileRemovalPlan, deletedName string) error {
	locked, err := LockAuthority(ctx, tx, organizationID, actor, accountID)
	if err != nil {
		return err
	}
	if err := locked.RequireUnshared(); err != nil {
		return err
	}
	if err := locked.RequireActive(); err != nil {
		return err
	}
	if deletedName != plan.Profile.Name {
		return &ProfileRevisionError{}
	}
	event := profileEvent(actor, organizationID, accountID, "people.profile_removed", plan.Profile.ID, locked, plan.Profile)
	event.AfterRevision = plan.Profile.Revision + 1
	event.Before = map[string]any{"name": plan.Profile.Name}
	event.After = map[string]any{"name": plan.Profile.Name}
	return RecordAudit(ctx, tx, event)
}
