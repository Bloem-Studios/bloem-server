package scanqueue

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

func TestNativeLibraryQueueFailClosed(t *testing.T) {
	var nilService *Service
	for _, s := range []*Service{nilService, {}, NewService(NewRepository(nil), nil, nil, nil, context.Background(), 1, 1)} {
		called := false
		run, created, err := s.EnqueueNativeLibraryAuthorized(context.Background(), 1, func(context.Context, pgx.Tx) error { called = true; return nil })
		var unavailable *catalog.NativeOnboardingError
		if run != nil || created || !errors.As(err, &unavailable) || unavailable.Code != "native_storage_unavailable" {
			t.Fatalf("missing dependency admitted: %+v %v %v", run, created, err)
		}
		if called {
			t.Fatal("missing dependency invoked authorizer")
		}
	}
}

func TestNativeLibraryQueueCoalescedSelectionRetry(t *testing.T) {
	cases := []struct {
		name, status string
		err          error
		want         bool
	}{
		{"accepted", "accepted", nil, false},
		{"running", "running", nil, false},
		{"completed", "completed", nil, true},
		{"failed", "failed", nil, true},
		{"cancelled", "cancelled", nil, true},
		{"unknown status", "unexpected", nil, true},
		{"no selected row", "", ErrScanRunNotFound, true},
		{"query error", "", errors.New("query failure"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var run *models.ScanRun
			if tt.status != "" {
				run = &models.ScanRun{ID: "selected", Status: tt.status}
			}
			if got := nativeQueueCoalesceRetry(run, tt.err); got != tt.want {
				t.Fatalf("retry=%v want=%v for %s", got, tt.want, tt.name)
			}
		})
	}
	if !nativeQueueCoalesceRetry(nil, nil) {
		t.Fatal("nil selected run claimed coalesced success")
	}
}
