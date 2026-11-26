package auth

// Bloem's writers of an account's sign-in rows, with the rule that keeps each
// from deadlocking with a plugin uninstall. Silo's
// TestEverySignInRowWriterKeepsTheLockOrder reads them from signInRowWriters,
// so it fails on a Bloem writer listed nowhere, and on one listed here that no
// longer writes those rows.
const (
	// Takes the account's identities, then writes several of its sessions,
	// and no device sign-in requests. An uninstall reaches sessions only
	// through the identities, so it waits at them or the writer waits for it.
	bloemIdentitiesThenSessions = "identities, then sessions; no requests"
)

var bloemSignInRowWriters = map[string]string{
	"internal/auth/bloem_session.go:revokeAllByUserWithQuerier":                            identitiesFirst,
	"internal/auth/bloem_repository.go:DeleteInTransaction":                                identitiesFirst,
	"internal/adminpeople/bloem_support_sessions.go:RevokeSessions":                        identitiesFirst,
	"internal/adminpeople/bloem_support_sessions.go:SignOutDevice":                         identitiesFirst,
	"internal/preview/settings.go:putInTransaction":                                        identitiesFirst,
	"internal/api/handlers/bloem_org_session_revocation.go:revokeOrganizationMemberAccess": bloemIdentitiesThenSessions,

	"internal/auth/bloem_session.go:revokeByUserAndSessionWithQuerier": oneRow,
	"internal/adminpeople/bloem_support_sessions.go:RevokeSession":     oneRow,
	"internal/api/bloem_org_session_revocation.go:endRevokedSession":   oneRow,
	"internal/auth/bloem_preview_guest.go:CreateInTx":                  oneRow,

	// Impersonation sessions and preview guest sessions carry no identity.
	"internal/auth/bloem_session.go:revokeAllByImpersonatorWithQuerier": noIdentityRows,
	"internal/preview/exchange.go:Exchange":                             noIdentityRows,
	"internal/preview/settings.go:revokeGuests":                         noIdentityRows,

	// Moving a profile to another account deletes it from the first, which
	// clears the requests approved for it, as Silo's profile delete does.
	"internal/userstore/pgstore/bloem_profile_move.go:MoveProfileInTx": knownGap,
}

func init() {
	for writer, rule := range bloemSignInRowWriters {
		signInRowWriters[writer] = rule
	}
}
