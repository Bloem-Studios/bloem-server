package nativestorage

import (
	"context"
	"errors"
	"testing"

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
