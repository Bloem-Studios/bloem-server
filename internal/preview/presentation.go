package preview

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Presentation gives clients browse-only display facts; it grants no authority.
type Presentation struct {
	Kind          string     `json:"kind" enum:"guest,personal"`
	EndsAt        *time.Time `json:"ends_at" nullable:"true" doc:"Absolute guest session expiry, or the personal end date supplied by the storefront; null until supplied."`
	CTAURL        string     `json:"cta_url" format:"uri-reference"`
	CTALabel      string     `json:"cta_label"`
	ItemCTALabel  string     `json:"item_cta_label" doc:"Operator wording for the title-page button; empty means the client's own (Subscribe to watch, read or listen)."`
	CTAMessage    string     `json:"cta_message" doc:"Operator sentence for the subscribe dialog; empty means the client's own."`
	StorefrontURL string     `json:"storefront_url" format:"uri-reference"`
	LaunchURL     string     `json:"launch_url" format:"uri-reference"`
}

func (s *Store) Presentation(ctx context.Context, account int, session string, guest bool) (*Presentation, error) {
	settings, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &Presentation{Kind: "personal", CTAURL: settings.CTAURL, CTALabel: settings.CTALabel, ItemCTALabel: settings.ItemCTALabel, CTAMessage: settings.CTAMessage, StorefrontURL: settings.StorefrontURL, LaunchURL: settings.LaunchURL}
	if out.CTAURL == "" {
		out.CTAURL = settings.StorefrontURL
	}
	if out.CTALabel == "" {
		out.CTALabel = "Subscribe"
		if strings.TrimSpace(settings.Brand) != "" {
			out.CTALabel += " to " + settings.Brand
		}
	}
	if guest {
		out.Kind = "guest"
		var expires time.Time
		err = s.pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE id=$1 AND user_id=$2 AND auth_method='preview_guest' AND revoked_at IS NULL AND expires_at>now()`, session, account).Scan(&expires)
		out.EndsAt = &expires
	} else {
		err = s.pool.QueryRow(ctx, `SELECT (SELECT ends_at FROM bloem_preview_personal_presentation WHERE account_id=$1)`, account).Scan(&out.EndsAt)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetPersonalPresentation changes display metadata only. Account expiry remains
// with the storefront and existing account administration.
func (a *Admin) SetPersonalPresentation(ctx context.Context, actor int, issuer string, account int, ends *time.Time) error {
	tx, settings, err := a.begin(ctx, actor, issuer)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if issuer == "" || settings.Issuer == "" || account <= 0 || account == settings.AccountID || ends != nil && (ends.IsZero() || ends.Year() < 1970 || ends.Year() > 9999) {
		return ErrInvalidSettings
	}
	var role string
	if err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 FOR UPDATE`, account).Scan(&role); err != nil {
		return ErrInvalidSettings
	}
	// Match the same current principal predicate used by auth, inside this write transaction.
	var marked bool
	err = tx.QueryRow(ctx, `SELECT $2<>'admin' AND (
 EXISTS(SELECT 1 FROM organization_memberships m JOIN users pu ON pu.id = m.account_id JOIN access_groups g ON g.organization_id=m.organization_id AND g.id=pu.access_group_id WHERE m.account_id=$1 AND m.status='active' AND g.managed_template_key='bloem-preview')
 OR EXISTS(SELECT 1 FROM user_profiles p JOIN access_groups g ON g.organization_id=p.organization_id AND g.id=p.access_group_id WHERE p.user_id=$1 AND g.managed_template_key='bloem-preview'))`, account, role).Scan(&marked)
	if err != nil {
		return err
	}
	if !marked {
		return fmt.Errorf("%w: account is not a personal preview", ErrInvalidSettings)
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_preview_personal_presentation(account_id,ends_at) VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET ends_at=EXCLUDED.ends_at,updated_at=now()`, account, ends)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
