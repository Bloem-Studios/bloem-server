package playback

import "context"

// restoreReconstructedClient asks the host for the client a recipe card does
// not name. A stream token's card never carries client metadata, so without
// this a session rebuilt after a restart shows no device in the admin views
// and records none in its history. A card that names its client (the stored
// card of a header-authenticated session) is left as it is.
func (m *TranscodeManager) restoreReconstructedClient(ctx context.Context, card *RecipeCard) {
	if m.RestoreReconstructedClient == nil || card == nil {
		return
	}
	if card.ClientName != "" || card.ClientUserAgent != "" || card.DeviceID != "" {
		return
	}
	m.RestoreReconstructedClient(ctx, card)
}
