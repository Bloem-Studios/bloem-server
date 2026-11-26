package auth

// Bloem's writers of an account's sign-in rows, with the rule that keeps each
// from deadlocking with a plugin uninstall. The writers that sign an account
// out everywhere withdraw its device sign-in approvals first, with Silo's
// statement (BloemWithdrawDeviceSignIns). Silo's
// TestEverySignInRowWriterKeepsTheLockOrder reads them from signInRowWriters,
// so it fails on a Bloem writer listed nowhere, and on one listed here that no
// longer writes those rows.
var bloemSignInRowWriters = map[string]string{
	"internal/auth/bloem_session.go:revokeAllByUserWithQuerier":                            identitiesFirst,
	"internal/auth/bloem_repository.go:DeleteInTransaction":                                identitiesFirst,
	"internal/adminpeople/bloem_support_sessions.go:RevokeSessions":                        identitiesFirst,
	"internal/adminpeople/bloem_support_sessions.go:SignOutDevice":                         identitiesFirst,
	"internal/preview/settings.go:putInTransaction":                                        identitiesFirst,
	"internal/api/handlers/bloem_org_session_revocation.go:revokeOrganizationMemberAccess": identitiesFirst,

	"internal/auth/bloem_session.go:revokeByUserAndSessionWithQuerier": oneRow,
	"internal/adminpeople/bloem_support_sessions.go:RevokeSession":     oneRow,
	"internal/api/bloem_org_session_revocation.go:endRevokedSession":   oneRow,
	"internal/auth/bloem_preview_guest.go:CreateInTx":                  oneRow,
	// Test support: a test's cleanup deletes the one request it started.
	"internal/bloemtestdevice/device.go:approve": oneRow,

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
