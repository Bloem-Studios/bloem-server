package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Bloem-owned. An administrative context token outlives the request that
// minted it by up to AdminContextTokenLifetime, and it names the account by
// both its numeric ID and its incarnation. Every request that presents one has
// to confirm the account standing behind it still exists, is still enabled,
// and is still the same incarnation -- otherwise a disabled organization
// administrator keeps acting until the token expires, and a token minted for
// an account that was since replaced under the same numeric ID acts for the
// replacement.

var (
	// ErrOperatorIneligible reports that the account behind an administrative
	// context is missing, disabled, or a different incarnation. Callers treat
	// it as stale authorization state.
	ErrOperatorIneligible = errors.New("administrative operator is not eligible")
	// ErrOperatorAuthorityUnavailable reports that operator standing could not
	// be determined. Callers must deny rather than assume.
	ErrOperatorAuthorityUnavailable = errors.New("administrative operator authority is unavailable")
)

// OperatorAuthority is the durable account standing behind an administrative
// context, read fresh for each request.
type OperatorAuthority struct {
	AccountID            int
	AccountIncarnationID uuid.UUID
	PlatformAdmin        bool
}

// OperatorAuthorityResolver resolves an administrative operator's current
// standing. Implementations fail closed: anything short of a positively
// matching, enabled account is an error.
type OperatorAuthorityResolver interface {
	ResolveOperator(ctx context.Context, accountID int, accountIncarnationID uuid.UUID) (OperatorAuthority, error)
}

// ResolveOperatorAuthority resolves operator standing through authorizer.
//
// PlatformAdminAuthorizer stays a one-method interface for its non-Bloem
// callers, so the resolver is an optional capability. An authorizer that does
// not provide it reports ErrOperatorAuthorityUnavailable: a missing capability
// denies administrative access instead of skipping the account check.
func ResolveOperatorAuthority(ctx context.Context, authorizer PlatformAdminAuthorizer, accountID int, accountIncarnationID uuid.UUID) (OperatorAuthority, error) {
	resolver, ok := authorizer.(OperatorAuthorityResolver)
	if !ok || resolver == nil {
		return OperatorAuthority{}, ErrOperatorAuthorityUnavailable
	}
	authority, err := resolver.ResolveOperator(ctx, accountID, accountIncarnationID)
	if err != nil {
		if errors.Is(err, ErrOperatorIneligible) || errors.Is(err, ErrOperatorAuthorityUnavailable) {
			return OperatorAuthority{}, err
		}
		return OperatorAuthority{}, fmt.Errorf("%w: %w", ErrOperatorAuthorityUnavailable, err)
	}
	if authority.AccountID != accountID || authority.AccountIncarnationID != accountIncarnationID {
		return OperatorAuthority{}, ErrOperatorIneligible
	}
	return authority, nil
}

func (a *platformAdminAuthorizer) ResolveOperator(ctx context.Context, accountID int, accountIncarnationID uuid.UUID) (OperatorAuthority, error) {
	if a == nil || a.accounts == nil {
		return OperatorAuthority{}, ErrOperatorAuthorityUnavailable
	}
	if accountID <= 0 || accountIncarnationID == uuid.Nil {
		return OperatorAuthority{}, ErrOperatorIneligible
	}
	account, err := a.accounts.GetByID(ctx, accountID)
	if IsNotFound(err) {
		return OperatorAuthority{}, ErrOperatorIneligible
	}
	if err != nil {
		return OperatorAuthority{}, fmt.Errorf("%w: load account: %w", ErrOperatorAuthorityUnavailable, err)
	}
	if account == nil || account.ID != accountID || !account.Enabled || account.AccountIncarnationID != accountIncarnationID {
		return OperatorAuthority{}, ErrOperatorIneligible
	}
	return OperatorAuthority{
		AccountID:            accountID,
		AccountIncarnationID: accountIncarnationID,
		PlatformAdmin:        account.Role == "admin",
	}, nil
}
