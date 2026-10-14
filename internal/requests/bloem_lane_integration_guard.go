package requests

// Bloem-owned. Bloem's audiobook request lane stores every credential of a
// connection in one composite api_key_ref ("abk1."), and the URLs those
// credentials belong to live in plugin_config, not base_url. Silo's generic
// request-integration editor keeps the stored api_key_ref whenever base_url
// is unchanged, so an edit through it could point a stored qBittorrent,
// NZBGet, abook or AI credential at another host. The audiobook plugin
// refuses a credential whose bound URL changed, but the server must not
// depend on one plugin's check: generic edits and option probes of an
// audiobook connection are refused here, and Bloem's audiobook setup, which
// recomposes the credential bindings itself, is the only writer.
//
// Generic creation stays open: it carries the caller's own credentials, not
// stored ones.

import (
	"context"
	"errors"
	"slices"
	"strings"
)

const (
	bloemAudiobookLaneMediaType = "audiobook"
	// Matches bookrequests.AudiobookRequestPluginID; this package cannot import
	// bookrequests.
	bloemAudiobookLanePluginID = "bloem.requests.audiobooks"
)

// errBloemLaneIntegration tells the generic editor where the connection is
// managed. It is a ValidationError so both API versions render it inline.
var errBloemLaneIntegration = &ValidationError{FormError: "Audiobook acquisition connections are edited in Bloem's audiobook acquisition setup."}

type bloemLaneWriterKey struct{}

// WithBloemLaneIntegrationWriter marks ctx as Bloem's own request-lane setup,
// which composes credential bindings server-side. Only Go callers can set it;
// no request header or body reaches it.
func WithBloemLaneIntegrationWriter(ctx context.Context) context.Context {
	return context.WithValue(ctx, bloemLaneWriterKey{}, true)
}

func bloemLaneIntegrationWriter(ctx context.Context) bool {
	allowed, _ := ctx.Value(bloemLaneWriterKey{}).(bool)
	return allowed
}

// installationPluginReader is the optional store capability that names the
// plugin behind an installation. Stores without it (test fakes) are judged on
// media types alone.
type installationPluginReader interface {
	InstallationPluginID(ctx context.Context, installationID int) (string, error)
}

// bloemLaneIntegration reports whether in belongs to the audiobook lane.
func (s *Service) bloemLaneIntegration(ctx context.Context, in Integration) (bool, error) {
	if slices.Contains(in.SupportedMediaTypes, bloemAudiobookLaneMediaType) {
		return true, nil
	}
	reader, ok := s.store.(installationPluginReader)
	if !ok || in.InstallationID == nil {
		return false, nil
	}
	plugin, err := reader.InstallationPluginID(ctx, *in.InstallationID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return plugin == bloemAudiobookLanePluginID, err
}

// guardBloemLaneIntegration refuses a generic edit that targets an audiobook
// lane connection, either as stored or as submitted. incoming is false for
// option probes, which only matter when they reuse a stored credential.
func (s *Service) guardBloemLaneIntegration(ctx context.Context, in Integration, incoming bool) error {
	if bloemLaneIntegrationWriter(ctx) {
		return nil
	}
	if incoming {
		lane, err := s.bloemLaneIntegration(ctx, in)
		if err != nil {
			return err
		}
		if lane {
			return errBloemLaneIntegration
		}
	}
	id := strings.TrimSpace(in.ID)
	if id == "" || id == "new" {
		return nil
	}
	stored, err := s.store.GetIntegration(ctx, id)
	if errors.Is(err, ErrNotFound) || err == nil && stored == nil {
		return nil
	}
	if err != nil {
		return err
	}
	lane, err := s.bloemLaneIntegration(ctx, *stored)
	if err != nil {
		return err
	}
	if lane {
		return errBloemLaneIntegration
	}
	return nil
}
