package nativestorage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
)

func TestHostShutdownWaitsForNativeReadersAndClosesAdmission(t *testing.T) {
	h := &Host{Manager: storageplugin.NewManager(storageplugin.Config{})}
	release, err := h.AcquireOpen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = h.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown=%v", err)
	}
	if _, err = h.AcquireOpen(); !errors.Is(err, storageplugin.ErrClosed) {
		t.Fatalf("admission=%v", err)
	}
	release()
	release()
	if err = h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A catalog source setting that fails its checks stops startup instead of
// leaving the server reading an unchecked host.
func TestNewHostRefusesAnUnsafeCatalogSource(t *testing.T) {
	t.Setenv(plugins.NativeCatalogURLEnv, "http://plugins.example.org/plugins.json")
	if _, err := NewHost(nil, nil, "/var/lib/bloem/native-storage", ""); err == nil || !strings.Contains(err.Error(), plugins.NativeCatalogURLEnv) {
		t.Fatalf("NewHost = %v, want an error naming %s", err, plugins.NativeCatalogURLEnv)
	}
}
