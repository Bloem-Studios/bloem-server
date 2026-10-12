package libraryingest

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestNativeAudioDefaultAndCompositionHold(t *testing.T) {
	c := &StorageScanner{resources: resourcetenancy.NewStore(nil)}
	if err := c.ConfigureNativeAudio(true, &nativestorage.ScanReader{}, scanner.NativeProbeConfig{}, new(atomic.Bool)); err == nil {
		t.Fatal("unconfigured audio scanner admitted")
	}
	c.nativeAudioReady = true
	c.audioGate = new(atomic.Bool)
	folder := &models.MediaFolder{ID: 1, Type: "audiobooks", Enabled: true}
	if _, err := c.ScanStorageFolder(context.Background(), folder, storagesource.LocationSelection{Location: storagesource.Location{FolderID: 1}}); err == nil {
		t.Fatal("unlatched delivery admitted scan")
	}
	if err := c.ConfigureNativeAudio(false, nil, scanner.NativeProbeConfig{}); err != nil {
		t.Fatal(err)
	}
}
