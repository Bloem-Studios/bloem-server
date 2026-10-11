package api

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bloemEbookSetup struct {
	pool     *pgxpool.Pool
	books    *bookrequests.Service
	requests *requests.Service
	platform auth.PlatformAdminAuthorizer
}

func (s bloemEbookSetup) installations(ctx context.Context, v bookrequests.Viewer) ([]handlers.EbookRequestInstallation, error) {
	rows, err := s.pool.Query(ctx, `
 SELECT i.id,c.capability_id FROM plugin_installations i
 JOIN plugin_capabilities c ON c.plugin_installation_id=i.id AND c.capability_type='request_router.v1'
 JOIN resource_owners ro ON ro.id=i.owner_id
 WHERE i.enabled AND i.kind='plugin' AND i.plugin_id='bloem.requests.bookwarehouse'
 AND (ro.kind='platform' OR (ro.kind='organization' AND ro.organization_id=$1))
 ORDER BY i.id,c.capability_id`, v.OrganizationID)
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
		i.Name = "Bookwarehouse"
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s bloemEbookSetup) List(ctx context.Context, v bookrequests.Viewer) (handlers.EbookRequestSetup, error) {
	integrations, err := s.books.GetEbookIntegrationLibraries(ctx, v)
	if err != nil {
		return handlers.EbookRequestSetup{}, err
	}
	libraries, err := s.books.EbookLibraries(ctx, v)
	if err != nil {
		return handlers.EbookRequestSetup{}, err
	}
	installations, err := s.installations(ctx, v)
	if err != nil {
		return handlers.EbookRequestSetup{}, err
	}
	enabled, err := catalog.NewServerSettingsRepo(s.pool).Get(ctx, config.EbookRequestsEnabledSettingKey)
	if err != nil {
		return handlers.EbookRequestSetup{}, err
	}
	return handlers.EbookRequestSetup{Available: true, Enabled: enabled == "true", Integrations: integrations, Libraries: libraries, Installations: installations}, nil
}

func (s bloemEbookSetup) SetEnabled(ctx context.Context, v bookrequests.Viewer, enabled bool) error {
	if err := s.requirePlatform(ctx, v); err != nil {
		return err
	}
	return catalog.NewServerSettingsRepo(s.pool).UpdateAtomicInTransaction(ctx, func(_ map[string]string, tx pgx.Tx) (map[string]string, error) {
		// Recheck durable authority under row locks before the policy write.
		var id int
		err := tx.QueryRow(ctx, `SELECT u.id FROM users u
 JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=$2 AND m.status='active'
 JOIN organizations o ON o.id=m.organization_id AND o.is_default AND (o.status='active' OR o.status='initializing')
 WHERE u.id=$1 AND u.enabled AND u.role='admin' FOR SHARE OF u,m,o`, v.UserID, v.OrganizationID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, bookrequests.ErrForbidden
		}
		if err != nil {
			return nil, err
		}
		return map[string]string{config.EbookRequestsEnabledSettingKey: strconv.FormatBool(enabled)}, nil
	})
}

func (s bloemEbookSetup) requirePlatform(ctx context.Context, v bookrequests.Viewer) error {
	if s.platform == nil || !v.Admin {
		return bookrequests.ErrForbidden
	}
	allowed, err := s.platform.IsPlatformAdmin(ctx, v.UserID)
	if err != nil {
		return errors.New("ebook request configuration authority unavailable")
	}
	if !allowed {
		return bookrequests.ErrForbidden
	}
	// Library discovery performs the durable default-organization, active
	// membership/account checks even when acquisition is opted out.
	_, err = s.books.EbookLibraries(ctx, v)
	return err
}

func (s bloemEbookSetup) Create(ctx context.Context, v bookrequests.Viewer, in handlers.EbookRequestConnectionInput) (bookrequests.EbookIntegration, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return bookrequests.EbookIntegration{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.Name == "" || len(in.Name) > 500 || in.BaseURL == "" || len(in.BaseURL) > 4096 || in.APIKey == "" || len(in.APIKey) > 8192 || in.InstallationID <= 0 {
		return bookrequests.EbookIntegration{}, bookrequests.ErrInvalid
	}
	installations, err := s.installations(ctx, v)
	if err != nil {
		return bookrequests.EbookIntegration{}, err
	}
	valid := false
	for _, installation := range installations {
		if installation.ID == in.InstallationID && installation.CapabilityID == in.CapabilityID {
			valid = true
			break
		}
	}
	if !valid {
		return bookrequests.EbookIntegration{}, bookrequests.ErrInvalid
	}
	connection, err := s.requests.CreateIntegration(ctx, requests.Viewer{UserID: v.UserID, IsAdmin: true}, requests.Integration{Name: in.Name, Enabled: true, BaseURL: in.BaseURL, APIKeyRef: in.APIKey, InstallationID: &in.InstallationID, CapabilityID: in.CapabilityID, SupportedMediaTypes: []string{"book"}, PluginConfig: map[string]any{"preferred_format": "epub", "allow_http": in.AllowHTTP}})
	if err != nil {
		if errors.Is(err, requests.ErrForbidden) {
			return bookrequests.EbookIntegration{}, bookrequests.ErrForbidden
		}
		return bookrequests.EbookIntegration{}, errors.New("ebook fulfillment connection could not be created")
	}
	return bookrequests.EbookIntegration{ID: connection.ID, Name: connection.Name}, nil
}

func (s bloemEbookSetup) SetLibrary(ctx context.Context, v bookrequests.Viewer, id string, libraryID int) (bookrequests.EbookIntegration, error) {
	if err := s.requirePlatform(ctx, v); err != nil {
		return bookrequests.EbookIntegration{}, err
	}
	return s.books.SetEbookIntegrationLibrary(ctx, v, id, libraryID)
}
