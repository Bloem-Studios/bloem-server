package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// System delivery types (docs/specs/client-engagement.md §A). Both carry a
// structured AlertBody in notification_deliveries.body instead of the
// catalog joins the release types use. Clients that predate them render the
// generic fallback row.
const (
	// DeliveryTypeSystemAlert is an operational, severity-tiered notice.
	DeliveryTypeSystemAlert = "system.alert"
	// DeliveryTypeSystemAnnouncement is an informational, admin-authored
	// message.
	DeliveryTypeSystemAnnouncement = "system.announcement"
)

// Alert severities. Critical alerts are never dismissible (AMENDMENT 2).
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Alert body size caps: titles must fit a push banner, bodies an inbox card.
const (
	alertTitleMaxLen = 120
	alertBodyMaxLen  = 2000
	alertCTAMaxLen   = 40
	alertURLMaxLen   = 2048
)

// ErrAlertBodyInvalid wraps every validation failure from NormalizeAlertBody.
var ErrAlertBodyInvalid = errors.New("invalid alert body")

// AlertCTA is the optional call-to-action button on an alert.
type AlertCTA struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// AlertBody is the structured payload stored in notification_deliveries.body
// for system.alert / system.announcement rows and projected verbatim onto
// DeliveryRowPayload.
type AlertBody struct {
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Severity    string     `json:"severity"`
	Deeplink    string     `json:"deeplink,omitempty"`
	ImageURL    string     `json:"image_url,omitempty"`
	Dismissible bool       `json:"dismissible"`
	CTA         *AlertCTA  `json:"cta,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// SupportedDeliveryTypes lists every delivery type this server can emit, for
// the capability payload. Order is stable for golden-friendly output.
func SupportedDeliveryTypes() []string {
	return []string{
		DeliveryTypeEpisodeAvailable,
		DeliveryTypeRequestFulfilled,
		DeliveryTypeRequestApproved,
		DeliveryTypeRequestDeclined,
		DeliveryTypeWebhookAutoDisabled,
		DeliveryTypeSystemAlert,
		DeliveryTypeSystemAnnouncement,
	}
}

// IsSystemDeliveryType reports whether the type carries an AlertBody.
func IsSystemDeliveryType(deliveryType string) bool {
	return deliveryType == DeliveryTypeSystemAlert || deliveryType == DeliveryTypeSystemAnnouncement
}

// NormalizeAlertBody validates and canonicalizes an author-supplied body:
// trims text, bounds lengths, checks the severity vocabulary and URL fields,
// and forces dismissible=false at severity=critical (the write-time rule
// clients rely on — a critical alert can never arrive dismissible).
func NormalizeAlertBody(in AlertBody, now time.Time) (AlertBody, error) {
	out := in
	out.Title = strings.TrimSpace(out.Title)
	out.Body = strings.TrimSpace(out.Body)
	out.Severity = strings.ToLower(strings.TrimSpace(out.Severity))
	out.Deeplink = strings.TrimSpace(out.Deeplink)
	out.ImageURL = strings.TrimSpace(out.ImageURL)

	if out.Title == "" {
		return AlertBody{}, fmt.Errorf("%w: title is required", ErrAlertBodyInvalid)
	}
	if utf8.RuneCountInString(out.Title) > alertTitleMaxLen {
		return AlertBody{}, fmt.Errorf("%w: title exceeds %d characters", ErrAlertBodyInvalid, alertTitleMaxLen)
	}
	if utf8.RuneCountInString(out.Body) > alertBodyMaxLen {
		return AlertBody{}, fmt.Errorf("%w: body exceeds %d characters", ErrAlertBodyInvalid, alertBodyMaxLen)
	}
	switch out.Severity {
	case "":
		out.Severity = SeverityInfo
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return AlertBody{}, fmt.Errorf("%w: severity must be one of info, warning, critical", ErrAlertBodyInvalid)
	}
	if out.Severity == SeverityCritical {
		out.Dismissible = false
	}
	if out.Deeplink != "" && !validAlertLink(out.Deeplink) {
		return AlertBody{}, fmt.Errorf("%w: deeplink must be an absolute URL or an app path", ErrAlertBodyInvalid)
	}
	if out.ImageURL != "" && !validAlertHTTPURL(out.ImageURL) {
		return AlertBody{}, fmt.Errorf("%w: image_url must be an http(s) URL", ErrAlertBodyInvalid)
	}
	if out.CTA != nil {
		cta := AlertCTA{Label: strings.TrimSpace(out.CTA.Label), URL: strings.TrimSpace(out.CTA.URL)}
		if cta.Label == "" && cta.URL == "" {
			out.CTA = nil
		} else {
			if cta.Label == "" || cta.URL == "" {
				return AlertBody{}, fmt.Errorf("%w: cta requires both label and url", ErrAlertBodyInvalid)
			}
			if utf8.RuneCountInString(cta.Label) > alertCTAMaxLen {
				return AlertBody{}, fmt.Errorf("%w: cta label exceeds %d characters", ErrAlertBodyInvalid, alertCTAMaxLen)
			}
			if !validAlertLink(cta.URL) {
				return AlertBody{}, fmt.Errorf("%w: cta url must be an absolute URL or an app path", ErrAlertBodyInvalid)
			}
			out.CTA = &cta
		}
	}
	if out.ExpiresAt != nil {
		if out.ExpiresAt.IsZero() {
			out.ExpiresAt = nil
		} else {
			if !out.ExpiresAt.After(now) {
				return AlertBody{}, fmt.Errorf("%w: expires_at must be in the future", ErrAlertBodyInvalid)
			}
			expires := out.ExpiresAt.UTC()
			out.ExpiresAt = &expires
		}
	}
	return out, nil
}

// validAlertLink accepts absolute http(s) URLs and app-relative paths
// ("/item/abc"); anything else (javascript:, data:, protocol-relative) is
// rejected because clients open these without further vetting.
func validAlertLink(raw string) bool {
	if len(raw) > alertURLMaxLen {
		return false
	}
	if strings.HasPrefix(raw, "/") {
		return !strings.HasPrefix(raw, "//")
	}
	return validAlertHTTPURL(raw)
}

func validAlertHTTPURL(raw string) bool {
	if len(raw) > alertURLMaxLen {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// ParseAlertBody decodes a stored body column; (nil, false) when the row
// carries none or the JSON is unreadable (never fails a list).
func ParseAlertBody(raw []byte) (*AlertBody, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var body AlertBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false
	}
	return &body, true
}
