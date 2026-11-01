package orgdomains

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memorySettings is a server settings store in memory, with the batch read
// branding.Service prefers.
type memorySettings struct{ values map[string]string }

func newMemorySettings(values map[string]string) *memorySettings {
	if values == nil {
		values = map[string]string{}
	}
	return &memorySettings{values: values}
}

func (m *memorySettings) Get(_ context.Context, key string) (string, error) {
	return m.values[key], nil
}
func (m *memorySettings) Set(_ context.Context, key, value string) error {
	m.values[key] = value
	return nil
}
func (m *memorySettings) GetMany(_ context.Context, keys ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if v := m.values[key]; v != "" {
			out[key] = v
		}
	}
	return out, nil
}

// liveDirectory serves a snapshot whose branding map the writer below changes,
// as the store and the directory do together.
type liveDirectory struct {
	snap snapshot
	d    *Directory
}

func newLiveDirectory() *liveDirectory {
	l := &liveDirectory{snap: testSnapshot()}
	l.d = newDirectory(func(context.Context) (snapshot, error) { return l.snap, nil }, time.Hour)
	return l
}

func (l *liveDirectory) SetBrandingValue(_ context.Context, org uuid.UUID, key, value string) error {
	if l.snap.branding[org] == nil {
		l.snap.branding[org] = map[string]string{}
	}
	if value == "" {
		delete(l.snap.branding[org], key)
	} else {
		l.snap.branding[org][key] = value
	}
	return nil
}

func onHost(org uuid.UUID) context.Context {
	return WithRequestOrganization(context.Background(), org)
}

func TestOverlayAnswersOrganizationBranding(t *testing.T) {
	live := newLiveDirectory()
	o := newSettingsOverlay(newMemorySettings(map[string]string{"branding.server_name": "Strm"}), live.d, live, OverlayOptions{})
	if got, _ := o.Get(onHost(testOrg2), "branding.server_name"); got != "Main" {
		t.Fatalf("org 2 server name = %q, want Main", got)
	}
	if got, _ := o.Get(context.Background(), "branding.server_name"); got != "Strm" {
		t.Fatalf("no organization: %q, want the server's Strm", got)
	}
}

func TestOverlayUnsetKeyUsesBuiltInDefault(t *testing.T) {
	live := newLiveDirectory()
	o := newSettingsOverlay(newMemorySettings(map[string]string{"branding.mark_ref": "server-mark.webp"}), live.d, live, OverlayOptions{})
	if got, _ := o.Get(onHost(testOrg2), "branding.mark_ref"); got != "" {
		t.Fatalf("org 2 without a mark got %q, want none (never the server's)", got)
	}
	values, err := o.GetMany(onHost(testOrg2), "branding.mark_ref", "branding.server_name")
	if err != nil || values["branding.mark_ref"] != "" || values["branding.server_name"] != "Main" {
		t.Fatalf("GetMany = %v, %v", values, err)
	}
}

func TestOverlayPassesOtherKeysThrough(t *testing.T) {
	live := newLiveDirectory()
	o := newSettingsOverlay(newMemorySettings(map[string]string{"smtp.host": "mail.example.org"}), live.d, live, OverlayOptions{PublicURL: true})
	if got, _ := o.Get(onHost(testOrg2), "smtp.host"); got != "mail.example.org" {
		t.Fatalf("smtp.host = %q", got)
	}
}

func TestOverlayDefaultOrganizationUsesServer(t *testing.T) {
	live := newLiveDirectory()
	live.snap.branding[testDefaultOrg] = map[string]string{"branding.server_name": "Ignored"}
	o := newSettingsOverlay(newMemorySettings(map[string]string{"branding.server_name": "Strm", "server.public_url": "https://lib.example.org"}), live.d, live, OverlayOptions{PublicURL: true})
	if got, _ := o.Get(onHost(testDefaultOrg), "branding.server_name"); got != "Strm" {
		t.Fatalf("default organization server name = %q, want the server's", got)
	}
	if got, _ := o.Get(onHost(testDefaultOrg), "server.public_url"); got != "https://lib.example.org" {
		t.Fatalf("default organization public URL = %q", got)
	}
}

func TestOverlayPublicURLOnlyWhenEnabled(t *testing.T) {
	live := newLiveDirectory()
	server := newMemorySettings(map[string]string{"server.public_url": "https://lib.example.org"})
	if got, _ := newSettingsOverlay(server, live.d, live, OverlayOptions{}).Get(onHost(testOrg2), "server.public_url"); got != "https://lib.example.org" {
		t.Fatalf("without PublicURL: %q, want the server's", got)
	}
	if got, _ := newSettingsOverlay(server, live.d, live, OverlayOptions{PublicURL: true}).Get(onHost(testOrg2), "server.public_url"); got != "https://main.domain.com" {
		t.Fatalf("with PublicURL: %q, want https://main.domain.com", got)
	}
	plain := newMemorySettings(map[string]string{"server.public_url": "http://lib.example.org:8090"})
	if got, _ := newSettingsOverlay(plain, live.d, live, OverlayOptions{PublicURL: true}).Get(onHost(testOrg2), "server.public_url"); got != "http://main.domain.com" {
		t.Fatalf("scheme follows the server's: %q", got)
	}
}

func TestOverlayOrganizationWithoutPrimaryKeepsServerPublicURL(t *testing.T) {
	live := newLiveDirectory()
	delete(live.snap.primary, testOrg2)
	o := newSettingsOverlay(newMemorySettings(map[string]string{"server.public_url": "https://lib.example.org"}), live.d, live, OverlayOptions{PublicURL: true})
	if got, _ := o.Get(onHost(testOrg2), "server.public_url"); got != "https://lib.example.org" {
		t.Fatalf("public URL = %q, want the server's", got)
	}
}

func TestOverlayWritesWithoutMarkerPassThrough(t *testing.T) {
	live := newLiveDirectory()
	server := newMemorySettings(nil)
	o := newSettingsOverlay(server, live.d, live, OverlayOptions{})
	if err := o.Set(onHost(testOrg2), "branding.server_name", "Server-wide"); err != nil {
		t.Fatal(err)
	}
	if server.values["branding.server_name"] != "Server-wide" || live.snap.branding[testOrg2]["branding.server_name"] != "Main" {
		t.Fatalf("server = %q, org 2 = %q; a write without the marker must change only the server",
			server.values["branding.server_name"], live.snap.branding[testOrg2]["branding.server_name"])
	}
}

func TestOverlayWritesWithMarkerGoToOrganization(t *testing.T) {
	live := newLiveDirectory()
	server := newMemorySettings(nil)
	o := newSettingsOverlay(server, live.d, live, OverlayOptions{})
	live.d.Lookup(context.Background(), "x.example.com") // load once, so the write must invalidate
	ctx := WithBrandingWrite(context.Background(), testOrg2)
	if err := o.Set(ctx, "branding.accent_color", "#112233"); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.values["branding.accent_color"]; ok {
		t.Fatal("the organization's write reached the server")
	}
	if got, _ := o.Get(onHost(testOrg2), "branding.accent_color"); got != "#112233" {
		t.Fatalf("org 2 accent after its write = %q", got)
	}
	if err := o.Set(ctx, "smtp.host", "x"); err != nil || server.values["smtp.host"] != "x" {
		t.Fatalf("a non-branding key with the marker still goes to the server: %v", err)
	}
}

func TestOverlayWithoutDirectoryIsTheServer(t *testing.T) {
	server := newMemorySettings(map[string]string{"branding.server_name": "Strm"})
	o := NewSettingsOverlay(server, nil, nil, OverlayOptions{PublicURL: true})
	if got, _ := o.Get(onHost(testOrg2), "branding.server_name"); got != "Strm" {
		t.Fatalf("got %q", got)
	}
}
