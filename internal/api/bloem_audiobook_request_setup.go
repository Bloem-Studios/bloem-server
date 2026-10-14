package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
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
	// store reads saved connections with decrypted credentials; router runs
	// plugin connection tests. Both stay inside the host.
	store  ebookIntegrationReader
	router requests.RequestRouterProvider
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
	if s.store != nil {
		for i := range integrations {
			stored, err := s.store.GetIntegration(ctx, integrations[i].ID)
			if err != nil || stored == nil {
				// The binding view stays usable when one row cannot be
				// decrypted; its editor then starts from defaults.
				continue
			}
			integrations[i].PluginConfig = stored.PluginConfig
			secrets, _, _ := decodeAudiobookCredentials(stored.APIKeyRef)
			integrations[i].StoredCredentials = slices.Sorted(maps.Keys(secrets))
		}
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

// saved returns a connection that is eligible for this organization's
// audiobook lane, with its decrypted credentials. The lane listing decides
// eligibility, so a connection of another plugin or media type is not found.
func (s bloemAudiobookSetup) saved(ctx context.Context, v bookrequests.Viewer, id string) (bookrequests.AudiobookIntegrationSummary, *requests.Integration, error) {
	if s.store == nil {
		return bookrequests.AudiobookIntegrationSummary{}, nil, errors.New("audiobook acquisition connections unavailable")
	}
	integrations, err := s.books.ListAudiobookIntegrations(ctx, v)
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, nil, err
	}
	index := slices.IndexFunc(integrations, func(row bookrequests.AudiobookIntegrationSummary) bool { return row.ID == id })
	if index < 0 {
		return bookrequests.AudiobookIntegrationSummary{}, nil, bookrequests.ErrNotFound
	}
	stored, err := s.store.GetIntegration(ctx, id)
	if errors.Is(err, requests.ErrNotFound) {
		return bookrequests.AudiobookIntegrationSummary{}, nil, bookrequests.ErrNotFound
	}
	if err != nil || stored == nil {
		return bookrequests.AudiobookIntegrationSummary{}, nil, errors.New("audiobook acquisition connection unavailable")
	}
	summary := integrations[index]
	if stored.InstallationID == nil || *stored.InstallationID != summary.InstallationID || stored.CapabilityID != summary.CapabilityID {
		return bookrequests.AudiobookIntegrationSummary{}, nil, bookrequests.ErrConflict
	}
	return summary, stored, nil
}

// Update edits a saved connection in place, so its identity, binding and
// in-flight acquisitions survive a credential rotation. The credential bundle
// is recomposed here: a saved secret is kept only while its bound URL is
// unchanged, so no saved secret is ever bound to a new host.
func (s bloemAudiobookSetup) Update(ctx context.Context, v bookrequests.Viewer, id string, in handlers.AudiobookRequestConnectionUpdate) (bookrequests.AudiobookIntegrationSummary, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	summary, stored, err := s.saved(ctx, v, id)
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	name := stored.Name
	if in.Name != "" {
		name = strings.TrimSpace(in.Name)
		if name == "" || len(name) > 500 || !utf8.ValidString(name) {
			return bookrequests.AudiobookIntegrationSummary{}, bookrequests.ErrInvalid
		}
	}
	config := in.PluginConfig
	if config == nil {
		config = stored.PluginConfig
	}
	if _, exists := config["service_kind"]; exists {
		return bookrequests.AudiobookIntegrationSummary{}, bookrequests.ErrInvalid
	}
	credentials, err := recomposeAudiobookCredentials(in.Credentials, stored.APIKeyRef, config)
	if err != nil {
		return bookrequests.AudiobookIntegrationSummary{}, err
	}
	updated, err := s.requests.UpdateIntegrationConditional(requests.WithBloemLaneIntegrationWriter(ctx), requests.Viewer{UserID: v.UserID, IsAdmin: true}, requests.Integration{ID: stored.ID, Name: name, Enabled: stored.Enabled, InstallationID: stored.InstallationID, CapabilityID: stored.CapabilityID, SupportedMediaTypes: []string{"audiobook"}, APIKeyRef: credentials, PluginConfig: config}, stored.Revision)
	if err != nil {
		// The filter also covers the composite itself: its base64 body
		// decodes to every credential it carries.
		secrets, _, _ := decodeAudiobookCredentials(credentials)
		return bookrequests.AudiobookIntegrationSummary{}, audiobookUpdateError(err, append(slices.Collect(maps.Values(secrets)), credentials, stored.APIKeyRef))
	}
	summary.Name = updated.Name
	return summary, nil
}

// audiobookUpdateError maps a refused save. The plugin's validation text is
// shown to the administrator, with the same credential filter as a connection
// test.
func audiobookUpdateError(err error, secrets []string) error {
	var validation *requests.ValidationError
	switch {
	case errors.As(err, &validation):
		message := validation.FormError
		if fields := slices.Sorted(maps.Keys(validation.FieldErrors)); message == "" && len(fields) > 0 {
			message = fields[0] + ": " + validation.FieldErrors[fields[0]]
		}
		if message = pluginMessage(message, secrets); message == "" {
			return fmt.Errorf("%w: the plugin rejected these settings", bookrequests.ErrInvalid)
		}
		return fmt.Errorf("%w: the plugin rejected these settings: %s", bookrequests.ErrInvalid, message)
	case errors.Is(err, requests.ErrStaleRevision):
		return bookrequests.ErrConflict
	case errors.Is(err, requests.ErrNotFound):
		return bookrequests.ErrNotFound
	case errors.Is(err, requests.ErrForbidden):
		return bookrequests.ErrForbidden
	default:
		return errors.New("audiobook acquisition connection could not be updated")
	}
}

// Test runs the plugin's connection checks against the saved connection.
// The host refuses before any plugin call when a saved secret's binding no
// longer matches its URL, the same rule the plugin enforces.
func (s bloemAudiobookSetup) Test(ctx context.Context, v bookrequests.Viewer, id string) (handlers.RequestIntegrationTestResult, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return handlers.RequestIntegrationTestResult{}, err
	}
	_, stored, err := s.saved(ctx, v, id)
	if err != nil {
		return handlers.RequestIntegrationTestResult{}, err
	}
	secrets, refusal := boundAudiobookCredentials(stored.APIKeyRef, stored.PluginConfig)
	if refusal != "" {
		return handlers.RequestIntegrationTestResult{OK: false, Message: refusal}, nil
	}
	// The plugin receives the composite, so an echo of it is withheld too.
	return checkRequestConnection(ctx, s.router, *stored, append(secrets, stored.APIKeyRef))
}

// boundAudiobookCredentials returns a saved bundle's credential values when
// every one is still bound to the URL now in config, or the reason it is not.
func boundAudiobookCredentials(saved string, config map[string]any) ([]string, string) {
	secrets, bind, err := decodeAudiobookCredentials(saved)
	if err != nil {
		return nil, "The saved credentials cannot be read. Edit the connection and enter them again."
	}
	for key := range secrets {
		destination := audiobookSecretDestinations[key]
		raw, _ := config[destination].(string)
		if fingerprint := audiobookURLFingerprint(raw); fingerprint == "" || fingerprint != bind[destination] {
			return nil, "A saved credential no longer matches its URL. Edit the connection and enter it again."
		}
	}
	return slices.Collect(maps.Values(secrets)), ""
}

// Credential keys of the plugin's composite format (abk1).
const (
	abkQBittorrentPassword = "qbittorrent_password"
	abkNZBGetPassword      = "nzbget_password"
	abkAbookEmail          = "abook_email"
	abkAbookPassword       = "abook_password"
	abkAIAPIKey            = "ai_api_key"
)

// audiobookSecretDestinations names the plugin_config URL each credential is
// bound to.
var audiobookSecretDestinations = map[string]string{abkQBittorrentPassword: "qbittorrent_url", abkNZBGetPassword: "nzbget_url", abkAbookEmail: "abook_base_url", abkAbookPassword: "abook_base_url", abkAIAPIKey: "ai_base_url"}

// audiobookSecretLabels names credentials in admin-facing errors.
var audiobookSecretLabels = map[string]string{abkQBittorrentPassword: "qBittorrent password", abkNZBGetPassword: "NZBGet password", abkAbookEmail: "abook.link email", abkAbookPassword: "abook.link password", abkAIAPIKey: "AI API key"}

// audiobookCredentialBundle binds secrets to their configured destinations. The
// ordinary request repository encrypts the resulting API key value at rest.
func audiobookCredentialBundle(credentials map[string]string, config map[string]any) (string, error) {
	secrets := map[string]string{}
	for key, value := range credentials {
		if _, known := audiobookSecretDestinations[key]; !known || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return "", bookrequests.ErrInvalid
		}
		if value != "" {
			secrets[key] = value
		}
	}
	return composeAudiobookCredentials(secrets, config)
}

func composeAudiobookCredentials(secrets map[string]string, config map[string]any) (string, error) {
	bindings := map[string]string{}
	bundle := map[string]any{}
	if (secrets[abkAbookEmail] == "") != (secrets[abkAbookPassword] == "") {
		return "", fmt.Errorf("%w: enter both the abook.link email and password", bookrequests.ErrInvalid)
	}
	for key, value := range secrets {
		destination := audiobookSecretDestinations[key]
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

// decodeAudiobookCredentials reads a saved abk1 bundle into its secrets and
// URL bindings. Unknown keys are ignored rather than carried forward.
func decodeAudiobookCredentials(raw string) (map[string]string, map[string]string, error) {
	encoded, ok := strings.CutPrefix(raw, "abk1.")
	if !ok {
		return nil, nil, bookrequests.ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil, bookrequests.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil {
		return nil, nil, bookrequests.ErrInvalid
	}
	secrets, bind := map[string]string{}, map[string]string{}
	for key, value := range fields {
		if key == "bind" {
			if err = json.Unmarshal(value, &bind); err != nil {
				return nil, nil, bookrequests.ErrInvalid
			}
			continue
		}
		if _, known := audiobookSecretDestinations[key]; !known {
			continue
		}
		var secret string
		if err = json.Unmarshal(value, &secret); err != nil {
			return nil, nil, bookrequests.ErrInvalid
		}
		if secret != "" {
			secrets[key] = secret
		}
	}
	return secrets, bind, nil
}

// recomposeAudiobookCredentials applies an edit to a saved bundle. A new value
// replaces its credential. A blank one keeps the saved credential only while
// the fingerprint of its URL in config matches the saved binding; when the URL
// changed the admin must enter it again, and when the URL was cleared the
// credential is dropped. Kept credentials are bound to the same URL they were
// entered for, so no saved secret is ever bound to a new host.
func recomposeAudiobookCredentials(updates map[string]string, saved string, config map[string]any) (string, error) {
	merged := map[string]string{}
	for key, value := range updates {
		if _, known := audiobookSecretDestinations[key]; !known || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return "", bookrequests.ErrInvalid
		}
		if value != "" {
			merged[key] = value
		}
	}
	secrets, bind, err := decodeAudiobookCredentials(saved)
	if err != nil {
		// An unreadable bundle keeps nothing; every credential must be entered.
		secrets, bind = map[string]string{}, map[string]string{}
	}
	for _, key := range slices.Sorted(maps.Keys(secrets)) {
		if _, replaced := merged[key]; replaced {
			continue
		}
		destination := audiobookSecretDestinations[key]
		raw, _ := config[destination].(string)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if fingerprint := audiobookURLFingerprint(raw); fingerprint == "" || fingerprint != bind[destination] {
			return "", fmt.Errorf("%w: enter the %s again because its URL changed", bookrequests.ErrInvalid, audiobookSecretLabels[key])
		}
		merged[key] = secrets[key]
	}
	return composeAudiobookCredentials(merged, config)
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
