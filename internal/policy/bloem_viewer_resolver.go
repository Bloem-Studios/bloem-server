package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// TenantLibraryResolver resolves the media folders visible to an authoritative
// tenant context.
type TenantLibraryResolver interface {
	AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)
}

// queuedTenantLibraries is a TenantLibraryResolver that can queue its read on
// a batch sent to the pool it reads, as resourcetenancy.Store does.
type queuedTenantLibraries interface {
	QueueAvailableMediaFolderIDs(*pgx.Batch, tenancy.Context) func(pgx.BatchResults) ([]int, error)
	StoresIn(*pgxpool.Pool) bool
}

// queuedGroupProvider is a group provider whose ResolvePolicy reads
// access.QueueSubjectGroupPolicy's statement on a pool, as
// access.TenantGroupStore does.
type queuedGroupProvider interface {
	ResolvePolicy(context.Context, access.GroupSubject) (*access.GroupPolicy, error)
	StoresIn(*pgxpool.Pool) bool
}

// WithBloemTenancy turns on Bloem tenancy: every resolved scope then requires
// the request's complete tenant and is bounded by the libraries tenantLibraries
// makes available to it, failing closed otherwise. Without it the resolver
// keeps Silo's single-tenant behavior. Production wiring always sets it
// (TestProductionViewerResolversUseBloemTenancy).
//
// Resolve then answers through bloemResolve, never Silo's batch, which reads
// the account's group without the request tenant. With WithSharedTables,
// bloemResolve sends Bloem's reads to that pool as one batch.
func (r *ViewerResolver) WithBloemTenancy(tenantLibraries TenantLibraryResolver) *ViewerResolver {
	if tenantLibraries == nil {
		panic("policy: WithBloemTenancy requires a tenant library resolver")
	}
	r.tenantLibraries = tenantLibraries
	return r
}

// bloemResolve is Resolve under Bloem tenancy: the batched reads when they
// apply, otherwise each fact through its repository.
func (r *ViewerResolver) bloemResolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	if scope, handled, err := r.resolveBloemBatched(ctx, input); handled {
		return scope, err
	}
	return r.resolveEach(ctx, input)
}

// resolveBloemBatched reads what resolveEach reads under tenancy, in the same
// order, as one batch: the account, the profile, the profile's group in the
// request tenant, the profile's viewer preferences and the tenant's libraries.
// It judges them in resolveEach's order, so each answer and each refusal is
// the one resolveEach gives. A failed statement fails the batch's later ones
// too; where resolveEach would have gone on past it, handled is false and the
// caller reads each fact again. It handles nothing without a request tenant,
// a shared pool, or providers that read that pool.
func (r *ViewerResolver) resolveBloemBatched(ctx context.Context, input access.ResolveInput) (access.Scope, bool, error) {
	tenant, ok := tenancy.FromContext(ctx)
	libraries, queuedLibraries := r.tenantLibraries.(queuedTenantLibraries)
	groups, queuedGroups := r.groups.(queuedGroupProvider)
	if !ok || r.shared == nil || !queuedLibraries || !libraries.StoresIn(r.shared) ||
		(r.groups != nil && (!queuedGroups || !groups.StoresIn(r.shared))) {
		return access.Scope{}, false, nil
	}

	batch := &pgx.Batch{}
	readUser := auth.QueueUser(batch, input.UserID)
	var readProfile func(pgx.BatchResults) (*userstore.Profile, error)
	if input.ProfileID != "" {
		readProfile = pgstore.QueueProfile(batch, input.UserID, input.ProfileID)
	}
	var readGroup func(pgx.BatchResults) (*access.GroupPolicy, error)
	var subjectErr error
	if r.groups != nil {
		var subject access.GroupSubject
		if subject, subjectErr = access.GroupSubjectFromContext(ctx, input.UserID, input.ProfileID); subjectErr == nil {
			readGroup = access.QueueSubjectGroupPolicy(batch, subject)
		}
	}
	var readPreferences func(pgx.BatchResults) ([]userstore.SettingValue, error)
	if strings.TrimSpace(input.ProfileID) != "" {
		readPreferences = pgstore.QueueSettingValuesForResolution(batch, input.UserID, access.ViewerPreferenceQuery(input.ProfileID))
	}
	readLibraries := libraries.QueueAvailableMediaFolderIDs(batch, tenant)

	results := r.shared.SendBatch(ctx, batch)
	user, userErr := readUser(results)
	var profile *userstore.Profile
	var profileErr error
	if readProfile != nil {
		profile, profileErr = readProfile(results)
	}
	var group *access.GroupPolicy
	var groupErr error
	if readGroup != nil {
		group, groupErr = readGroup(results)
	}
	var preferences access.ViewerPreferenceRows
	if readPreferences != nil {
		preferences.Rows, preferences.Err = readPreferences(results)
	}
	tenantLibraryIDs, librariesErr := readLibraries(results)
	// Close reports the first failed read again; each read was checked above.
	_ = results.Close()

	if userErr != nil {
		return access.Scope{}, true, fmt.Errorf("loading user %d: %w", input.UserID, userErr)
	}
	if input.ProfileID != "" {
		if profileErr != nil {
			return access.Scope{}, true, fmt.Errorf("loading profile %s: %w", input.ProfileID, profileErr)
		}
		if profile == nil {
			return access.Scope{}, true, access.ErrProfileNotFound
		}
	}
	// access.EffectivePolicyForRequest with a tenant-scoped provider: the
	// subject comes from the request tenant, and an administrator's group is
	// never read.
	effective := access.ApplyGroupPolicy(user, nil)
	// cutOff is set where a failed statement cut off reads resolveEach would
	// still make: a group read resolveEach skips for an administrator, or the
	// viewer preferences, which resolveEach degrades and reads past. The
	// caller then reads each fact again.
	cutOff := preferences.Err != nil
	switch {
	case r.groups == nil:
	case subjectErr != nil:
		return access.Scope{}, true, fmt.Errorf("loading access group policy for user %d: %w", input.UserID, subjectErr)
	case user == nil || user.Role == models.RoleAdmin:
		cutOff = cutOff || (groupErr != nil && !errors.Is(groupErr, access.ErrGroupNotFound))
	case groupErr != nil:
		return access.Scope{}, true, fmt.Errorf("loading access group policy for user %d: %w", input.UserID, groupErr)
	default:
		effective = access.ApplyGroupPolicy(user, group)
	}
	if cutOff {
		return access.Scope{}, false, nil
	}
	viewerPreferences := access.ResolveViewerPreferences(ctx, preferences, r.defaults, input.ProfileID)
	scope, err := r.ResolveFacts(withPrefetchedTenantLibraries(ctx, tenant, tenantLibraryIDs, librariesErr),
		input, user, profile, effective, viewerPreferences)
	return scope, true, err
}

// prefetchedTenantLibraries carries resolveBloemBatched's read of a tenant's
// libraries into ResolveFacts.
type prefetchedTenantLibraries struct {
	tenant tenancy.Context
	ids    []int
	err    error
}

type prefetchedTenantLibrariesKey struct{}

func withPrefetchedTenantLibraries(ctx context.Context, tenant tenancy.Context, ids []int, err error) context.Context {
	return context.WithValue(ctx, prefetchedTenantLibrariesKey{}, prefetchedTenantLibraries{tenant: tenant, ids: ids, err: err})
}

// bloemTenantFacts returns the request's tenant facts when tenancy is on, and
// no facts in Silo mode.
func (r *ViewerResolver) bloemTenantFacts(ctx context.Context, userID int) (TenantFacts, error) {
	if r.tenantLibraries == nil {
		return TenantFacts{}, nil
	}
	facts, err := TenantFactsFromContext(ctx, userID)
	if err != nil {
		return TenantFacts{}, fmt.Errorf("resolve viewer scope tenant facts: %w", err)
	}
	return facts, nil
}

// resolveTenantLibraryIDs returns the media folders available to the
// validated request tenant, as resolveBloemBatched read them when it did.
func (r *ViewerResolver) resolveTenantLibraryIDs(ctx context.Context) ([]int, error) {
	if r.tenantLibraries == nil {
		return nil, nil
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("resolve viewer scope tenant libraries: %w", ErrTenantFactsUnavailable)
	}
	var ids []int
	var err error
	if prefetched, found := ctx.Value(prefetchedTenantLibrariesKey{}).(prefetchedTenantLibraries); found && prefetched.tenant == tenant {
		ids, err = prefetched.ids, prefetched.err
	} else {
		ids, err = r.tenantLibraries.AvailableMediaFolderIDs(ctx, tenant)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve viewer scope tenant libraries: %w", err)
	}
	return ids, nil
}
