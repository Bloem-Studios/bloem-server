package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UpgradePrompt is the alert a viewer gets when a stream is refused because
// their plan's simultaneous-stream limit is reached. The storefront sets the
// wording and the button's link; one row holds it for the installation.
type UpgradePrompt struct {
	Enabled   bool      `json:"enabled"`
	Title     string    `json:"title" maxLength:"80"`
	Body      string    `json:"body" maxLength:"240"`
	CTALabel  string    `json:"cta_label" maxLength:"40"`
	CTAURL    string    `json:"cta_url" doc:"HTTPS address the button opens, usually the storefront's plan page"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrUpgradePromptInvalid wraps every refused upgrade prompt.
var ErrUpgradePromptInvalid = errors.New("invalid upgrade prompt")

// upgradePromptLifetime is how long an unread prompt stays in the inbox.
const upgradePromptLifetime = 3 * 24 * time.Hour

func (p UpgradePrompt) normalize() (UpgradePrompt, error) {
	p.Title, p.Body = strings.TrimSpace(p.Title), strings.TrimSpace(p.Body)
	p.CTALabel, p.CTAURL = strings.TrimSpace(p.CTALabel), strings.TrimSpace(p.CTAURL)
	switch {
	case utf8.RuneCountInString(p.Title) > 80:
		return p, fmt.Errorf("%w: title is longer than 80 characters", ErrUpgradePromptInvalid)
	case utf8.RuneCountInString(p.Body) > 240:
		return p, fmt.Errorf("%w: body is longer than 240 characters", ErrUpgradePromptInvalid)
	case utf8.RuneCountInString(p.CTALabel) > 40:
		return p, fmt.Errorf("%w: button label is longer than 40 characters", ErrUpgradePromptInvalid)
	case p.CTAURL != "" && (!strings.HasPrefix(p.CTAURL, "https://") || !validAlertLink(p.CTAURL)):
		return p, fmt.Errorf("%w: the button link must be an https address", ErrUpgradePromptInvalid)
	case p.Enabled && (p.Title == "" || p.CTALabel == "" || p.CTAURL == ""):
		return p, fmt.Errorf("%w: a title, a button label and a link are required to turn it on", ErrUpgradePromptInvalid)
	}
	return p, nil
}

// LoadUpgradePrompt reads the installation's upgrade prompt; none is off.
func LoadUpgradePrompt(ctx context.Context, pool *pgxpool.Pool) (UpgradePrompt, error) {
	var p UpgradePrompt
	err := pool.QueryRow(ctx, `
		SELECT enabled, title, body, cta_label, cta_url, updated_at
		FROM bloem_upgrade_prompt WHERE id`).Scan(&p.Enabled, &p.Title, &p.Body, &p.CTALabel, &p.CTAURL, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UpgradePrompt{}, nil
	}
	if err != nil {
		return UpgradePrompt{}, fmt.Errorf("load upgrade prompt: %w", err)
	}
	return p, nil
}

// SaveUpgradePrompt replaces the installation's upgrade prompt.
func SaveUpgradePrompt(ctx context.Context, pool *pgxpool.Pool, p UpgradePrompt) (UpgradePrompt, error) {
	p, err := p.normalize()
	if err != nil {
		return UpgradePrompt{}, err
	}
	err = pool.QueryRow(ctx, `
		INSERT INTO bloem_upgrade_prompt (id, enabled, title, body, cta_label, cta_url, updated_at)
		VALUES (true, $1, $2, $3, $4, $5, now())
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, title = EXCLUDED.title,
			body = EXCLUDED.body, cta_label = EXCLUDED.cta_label, cta_url = EXCLUDED.cta_url, updated_at = now()
		RETURNING updated_at`, p.Enabled, p.Title, p.Body, p.CTALabel, p.CTAURL).Scan(&p.UpdatedAt)
	if err != nil {
		return UpgradePrompt{}, fmt.Errorf("save upgrade prompt: %w", err)
	}
	return p, nil
}

type upgradePromptBackend interface {
	upgradePrompt(context.Context) (UpgradePrompt, error)
	dispatchUpgradePrompt(ctx context.Context, userID int, profileID, deliveryID string, body AlertBody) error
}

// UpgradePromptNotifier turns a stream refused at the plan's limit into an
// inbox alert with the storefront's button: at most one per profile per UTC
// day, because the delivery ID is derived from the profile and the day.
type UpgradePromptNotifier struct {
	backend upgradePromptBackend
	now     func() time.Time
}

// NewUpgradePromptNotifier returns nil when notifications are unavailable.
func NewUpgradePromptNotifier(system *System) *UpgradePromptNotifier {
	if system == nil || system.pool == nil {
		return nil
	}
	return &UpgradePromptNotifier{backend: system, now: time.Now}
}

// StreamLimitReached is called on the refused request's path, so it returns
// at once and sends in the background.
func (n *UpgradePromptNotifier) StreamLimitReached(ctx context.Context, userID int, profileID string) {
	if n == nil || userID <= 0 || profileID == "" {
		return
	}
	dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		if err := n.notify(dispatchCtx, userID, profileID); err != nil {
			slog.WarnContext(dispatchCtx, "upgrade prompt failed", "user_id", userID, "error", err)
		}
	}()
}

func (n *UpgradePromptNotifier) notify(ctx context.Context, userID int, profileID string) error {
	prompt, err := n.backend.upgradePrompt(ctx)
	if err != nil || !prompt.Enabled {
		return err
	}
	now := n.now().UTC()
	expires := now.Add(upgradePromptLifetime)
	body := AlertBody{
		Title: prompt.Title, Body: prompt.Body, Severity: SeverityInfo, Dismissible: true,
		CTA: &AlertCTA{Label: prompt.CTALabel, URL: prompt.CTAURL}, ExpiresAt: &expires,
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("bloem-upgrade-prompt:%d:%s:%s",
		userID, profileID, now.Format(time.DateOnly)))).String()
	return n.backend.dispatchUpgradePrompt(ctx, userID, profileID, id, body)
}

func (s *System) upgradePrompt(ctx context.Context) (UpgradePrompt, error) {
	return LoadUpgradePrompt(ctx, s.pool)
}

// dispatchUpgradePrompt respects the profile's notification switch, then
// sends through the operational inbox and its push channels. A repeated
// delivery ID is ignored, which is the once-a-day limit.
func (s *System) dispatchUpgradePrompt(ctx context.Context, userID int, profileID, deliveryID string, body AlertBody) error {
	if s.Preferences != nil {
		prefs, err := s.Preferences.Get(ctx, userID, profileID)
		if err != nil {
			return err
		}
		if !prefs.Enabled {
			return nil
		}
	}
	normalized, err := NormalizeAlertBody(body, time.Now())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = s.DispatchOperational(ctx, Delivery{
		ID: deliveryID, UserID: userID, ProfileID: profileID, Type: DeliveryTypeSystemAlert, Body: raw,
	}, OperationalDispatch{})
	return err
}
