package handlers

import "github.com/Silo-Server/silo-server/internal/outbound"

// SetArtworkClient replaces the SSRF-guarded client collection artwork URL
// downloads go through. Bloem keeps it a setter so NewLibraryCollectionHandler
// keeps upstream's signature. A nil client is ignored.
func (h *LibraryCollectionHandler) SetArtworkClient(client *outbound.Client) {
	if h != nil && client != nil {
		h.artworkClient = client
	}
}
