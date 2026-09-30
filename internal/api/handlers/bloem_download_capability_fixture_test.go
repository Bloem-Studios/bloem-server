package handlers

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/downloads"
)

func (f *fakeDownloadService) CapabilityForProfile(_ context.Context, _ int, profileID string) (downloads.Capability, error) {
	f.gotCapProfile = profileID
	return f.capability, nil
}
