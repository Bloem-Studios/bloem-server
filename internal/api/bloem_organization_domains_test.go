package api

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/orgdomains"
)

type mailSettingsStore struct{}

func (mailSettingsStore) Get(context.Context, string) (string, error)       { return "", nil }
func (mailSettingsStore) Set(context.Context, string, string) error         { return nil }
func (mailSettingsStore) GetAll(context.Context) (map[string]string, error) { return nil, nil }

func TestAccountMailSettingsFollowTheDomainDirectory(t *testing.T) {
	settings := mailSettingsStore{}
	if got := bloemAccountMailSettings(Dependencies{}, settings); got != settings {
		t.Fatalf("without a directory account mail must read the server's settings, got %T", got)
	}
	deps := Dependencies{}
	deps.OrganizationDomains = orgdomains.NewDirectory(orgdomains.NewStore(nil), time.Minute)
	if _, ok := bloemAccountMailSettings(deps, settings).(*orgdomains.SettingsOverlay); !ok {
		t.Fatal("with a directory account mail must read through the organization overlay")
	}
}
