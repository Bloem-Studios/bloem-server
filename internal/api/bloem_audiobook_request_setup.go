package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

type bloemAudiobookSetup struct {
	pool     *pgxpool.Pool
	books    *bookrequests.Service
	requests *requests.Service
	platform auth.PlatformAdminAuthorizer
}

func (s bloemAudiobookSetup) installations(ctx context.Context, v bookrequests.Viewer) ([]handlers.EbookRequestInstallation, error) {
	rows, err := s.pool.Query(ctx, `SELECT i.id,c.capability_id FROM plugin_installations i JOIN plugin_capabilities c ON c.plugin_installation_id=i.id AND c.capability_type='request_router.v1' JOIN resource_owners ro ON ro.id=i.owner_id JOIN organizations o ON o.id=$1 WHERE i.enabled AND i.kind='plugin' AND i.plugin_id=$2 AND ((ro.kind='platform' AND o.is_default) OR (ro.kind='organization' AND ro.organization_id=o.id)) ORDER BY i.id,c.capability_id`, v.OrganizationID, bookrequests.AudiobookRequestPluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []handlers.EbookRequestInstallation{}
	for rows.Next() {
		var i handlers.EbookRequestInstallation
		if err = rows.Scan(&i.ID, &i.CapabilityID); err != nil {
			return nil, err
		}
		i.Name = "Audiobooks"
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s bloemAudiobookSetup) List(ctx context.Context, v bookrequests.Viewer) (handlers.AudiobookRequestSetup, error) {
	integrations, err := s.books.ListAudiobookIntegrations(ctx, v)
	if err != nil {
		return handlers.AudiobookRequestSetup{}, err
	}
	libraries, err := s.books.AudiobookLibraries(ctx, v)
	if err != nil {
		return handlers.AudiobookRequestSetup{}, err
	}
	installations, err := s.installations(ctx, v)
	if err != nil {
		return handlers.AudiobookRequestSetup{}, err
	}
	return handlers.AudiobookRequestSetup{Available: true, Integrations: integrations, Libraries: libraries, Installations: installations}, nil
}
func (s bloemAudiobookSetup) requirePlatform(ctx context.Context, v bookrequests.Viewer) error {
	if s.platform == nil || !v.Admin {
		return bookrequests.ErrForbidden
	}
	allowed, err := s.platform.IsPlatformAdmin(ctx, v.UserID)
	if err != nil {
		return errors.New("audiobook configuration authority unavailable")
	}
	if !allowed {
		return bookrequests.ErrForbidden
	}
	_, err = s.books.AudiobookLibraries(ctx, v)
	return err
}
func (s bloemAudiobookSetup) Create(ctx context.Context, v bookrequests.Viewer, in handlers.AudiobookRequestConnectionInput) (bookrequests.AudiobookIntegrationSummary, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 500 || !utf8.ValidString(in.Name) || in.InstallationID <= 0 {
		return bookrequests.AudiobookIntegrationSummary{}, bookrequests.ErrInvalid
	}
	if _, exists := in.PluginConfig["service_kind"]; exists {
		return bookrequests.AudiobookIntegrationSummary{}, bookrequests.ErrInvalid
	}
	installations, err := s.installations(ctx, v)
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	valid := false
	for _, i := range installations {
		if i.ID == in.InstallationID && i.CapabilityID == in.CapabilityID {
			valid = true
			break
		}
	}
	if !valid {
		return bookrequests.AudiobookIntegrationSummary{}, bookrequests.ErrInvalid
	}
	credentials, err := audiobookCredentialBundle(in.Credentials, in.PluginConfig)
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	c, err := s.requests.CreateIntegration(ctx, requests.Viewer{UserID: v.UserID, IsAdmin: true}, requests.Integration{Name: in.Name, Enabled: true, InstallationID: &in.InstallationID, CapabilityID: in.CapabilityID, SupportedMediaTypes: []string{"audiobook"}, APIKeyRef: credentials, PluginConfig: in.PluginConfig})
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, errors.New("audiobook acquisition connection could not be created")
	}
	return bookrequests.AudiobookIntegrationSummary{ID: c.ID, Name: c.Name, InstallationID: in.InstallationID, CapabilityID: in.CapabilityID, DownloadRoots: []string{}}, nil
}
func (s bloemAudiobookSetup) SetLibrary(ctx context.Context, v bookrequests.Viewer, id string, libraryID int, roots []string) (bookrequests.AudiobookIntegrationSummary, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	return s.books.SetAudiobookIntegrationLibrary(ctx, v, id, libraryID, roots)
}

// audiobookCredentialBundle binds secrets to their configured destinations. The
// ordinary request repository encrypts the resulting API key value at rest.
func audiobookCredentialBundle(credentials map[string]string, config map[string]any) (string, error) {
	bindings := map[string]string{}
	bundle := map[string]any{}
	keys := map[string]string{"qbittorrent_password": "qbittorrent_url", "nzbget_password": "nzbget_url", "abook_email": "abook_base_url", "abook_password": "abook_base_url", "ai_api_key": "ai_base_url"}
	for key, value := range credentials {
		destination, known := keys[key]
		if !known || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return "", bookrequests.ErrInvalid
		}
		if value == "" {
			continue
		}
		raw, ok := config[destination].(string)
		if !ok {
			return "", bookrequests.ErrInvalid
		}
		fingerprint := audiobookURLFingerprint(raw)
		if fingerprint == "" {
			return "", bookrequests.ErrInvalid
		}
		bundle[key] = value
		bindings[destination] = fingerprint
	}
	if len(bindings) > 0 {
		bundle["bind"] = bindings
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		return "", bookrequests.ErrInvalid
	}
	return "abk1." + base64.RawURLEncoding.EncodeToString(body), nil
}
func audiobookURLFingerprint(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if scheme == "https" && port == "443" || scheme == "http" && port == "80" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	sum := sha256.Sum256([]byte(scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")))
	return hex.EncodeToString(sum[:8])
}
