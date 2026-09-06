package playback

import "context"

// InitialReconciliationStoreV3 returns a bounded account inventory, not authority.
// Use the final binding's attempt ID as the exclusive next key. Restart at an
// empty key for each sweep; eligibility may change between pages.
type InitialReconciliationStoreV3 interface {
	ListInitialReconciliation(context.Context, int, string, int) ([]InitialActivationV3, error)
}
