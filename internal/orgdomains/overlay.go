package orgdomains

import (
	"context"
	"net/url"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/branding"
)

const publicURLKey = "server.public_url"

// SettingsBackend is the server settings store the overlay reads and writes
// through.
type SettingsBackend interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type settingsBatchReader interface {
	GetMany(ctx context.Context, keys ...string) (map[string]string, error)
}

type brandingWriter interface {
	SetBrandingValue(ctx context.Context, org uuid.UUID, key, value string) error
}

// OverlayOptions choose what the overlay answers besides branding.
type OverlayOptions struct {
	// PublicURL answers server.public_url with the organization's primary
	// domain, for the services that put links in emails.
	PublicURL bool
}

// SettingsOverlay answers the presentation settings for the organization a
// context belongs to (OrganizationFor), and everything else from the server.
// Silo's branding service and account mail read through it, so each domain
// shows its organization's name, logo and colours, and its emails link to it.
//
// For the default organization, or no organization, every key is the
// server's. For another organization, a presentation key is its own value or
// empty (Silo's built-in default), never the server's: the server's branding
// is the default organization's brand. A write is an organization's only when
// the context carries WithBrandingWrite; any other write is the server's.
type SettingsOverlay struct {
	inner  SettingsBackend
	d      *Directory
	writer brandingWriter
	opts   OverlayOptions
	keys   map[string]struct{}
}

func NewSettingsOverlay(inner SettingsBackend, d *Directory, store *Store, opts OverlayOptions) *SettingsOverlay {
	var writer brandingWriter
	if store != nil {
		writer = store
	}
	return newSettingsOverlay(inner, d, writer, opts)
}

func newSettingsOverlay(inner SettingsBackend, d *Directory, writer brandingWriter, opts OverlayOptions) *SettingsOverlay {
	keys := map[string]struct{}{}
	for _, key := range branding.PresentationSettingKeys() {
		keys[key] = struct{}{}
	}
	return &SettingsOverlay{inner: inner, d: d, writer: writer, opts: opts, keys: keys}
}

// organization is the non-default organization ctx presents, if any.
func (o *SettingsOverlay) organization(ctx context.Context) (uuid.UUID, bool) {
	if o.d == nil {
		return uuid.Nil, false
	}
	org, ok := OrganizationFor(ctx)
	if !ok || o.d.IsDefault(ctx, org) {
		return uuid.Nil, false
	}
	return org, true
}

// override is the organization's answer for key; handled is false when the
// server answers.
func (o *SettingsOverlay) override(ctx context.Context, org uuid.UUID, key string) (value string, handled bool) {
	if _, presentation := o.keys[key]; presentation {
		value, _ = o.d.BrandingValue(ctx, org, key)
		return value, true
	}
	if key == publicURLKey && o.opts.PublicURL {
		if host, ok := o.d.PrimaryHost(ctx, org); ok {
			return o.scheme(ctx) + "://" + host, true
		}
	}
	return "", false
}

// scheme is the server public URL's scheme, https when it has none.
func (o *SettingsOverlay) scheme(ctx context.Context) string {
	if raw, err := o.inner.Get(ctx, publicURLKey); err == nil {
		if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			return u.Scheme
		}
	}
	return "https"
}

func (o *SettingsOverlay) Get(ctx context.Context, key string) (string, error) {
	if org, ok := o.organization(ctx); ok {
		if value, handled := o.override(ctx, org, key); handled {
			return value, nil
		}
	}
	return o.inner.Get(ctx, key)
}

// GetMany is the batch read branding.Service prefers: one server read, then
// the organization's answers.
func (o *SettingsOverlay) GetMany(ctx context.Context, keys ...string) (map[string]string, error) {
	var values map[string]string
	if batch, ok := o.inner.(settingsBatchReader); ok {
		var err error
		if values, err = batch.GetMany(ctx, keys...); err != nil {
			return nil, err
		}
	} else {
		values = make(map[string]string, len(keys))
		for _, key := range keys {
			if v, err := o.inner.Get(ctx, key); err == nil && v != "" {
				values[key] = v
			}
		}
	}
	org, ok := o.organization(ctx)
	if !ok {
		return values, nil
	}
	for _, key := range keys {
		if value, handled := o.override(ctx, org, key); handled {
			if value == "" {
				delete(values, key)
			} else {
				values[key] = value
			}
		}
	}
	return values, nil
}

func (o *SettingsOverlay) Set(ctx context.Context, key, value string) error {
	if org, ok := BrandingWriteOrganization(ctx); ok && o.writer != nil {
		if _, presentation := o.keys[key]; presentation {
			if err := o.writer.SetBrandingValue(ctx, org, key, value); err != nil {
				return err
			}
			o.d.Invalidate()
			return nil
		}
	}
	return o.inner.Set(ctx, key, value)
}
