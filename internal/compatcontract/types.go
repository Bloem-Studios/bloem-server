// Package compatcontract executes protocol fixtures against a compatibility
// listener. It deliberately contains no Vondel-domain dependencies so the
// same fixtures can characterize embedded handlers and extracted sidecars.
package compatcontract

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// CredentialProvider attaches target-specific credentials to a request.
// Implementations must not retain reusable user credentials in fixtures.
type CredentialProvider interface {
	Apply(*http.Request) error
}

// CredentialFunc adapts a function into a CredentialProvider.
type CredentialFunc func(*http.Request) error

// Apply attaches credentials to req.
func (f CredentialFunc) Apply(req *http.Request) error { return f(req) }

// Target identifies one compatibility listener under test.
type Target struct {
	BaseURL     string
	Client      *http.Client
	Credentials CredentialProvider
}

// Suite is a named protocol surface characterized by Cases.
type Suite struct {
	Name  string
	Cases []Case
}

// Case records one observable protocol interaction. Fixture IDs and URLs must
// be invented and use reserved domains only.
type Case struct {
	Name                   string              `json:"name"`
	Method                 string              `json:"method,omitempty"`
	Path                   string              `json:"path"`
	Body                   []byte              `json:"body,omitempty"`
	Headers                map[string]string   `json:"headers,omitempty"`
	WantStatus             int                 `json:"want_status,omitempty"`
	WantHeaders            map[string]string   `json:"want_headers,omitempty"`
	WantJSON               json.RawMessage     `json:"want_json,omitempty"`
	WantSHA256             string              `json:"want_sha256,omitempty"`
	WantWebSocketJSON      []json.RawMessage   `json:"want_websocket_json,omitempty"`
	WantWebSocketNoMessage bool                `json:"want_websocket_no_message,omitempty"`
	Exception              string              `json:"exception,omitempty"`
	PresentStrings         []string            `json:"present_strings,omitempty"`
	AbsentStrings          []string            `json:"absent_strings,omitempty"`
	Timing                 *TimingDistribution `json:"timing,omitempty"`
}

// TimingDistribution compares a protected missing ID with an unrelated
// missing ID over samples. It avoids treating a single noisy elapsed time as
// proof of non-disclosure.
type TimingDistribution struct {
	ControlPath string  `json:"control_path"`
	Samples     int     `json:"samples"`
	MaxRatio    float64 `json:"max_ratio"`
}

const (
	ExceptionUnauthenticated = "unauthenticated"
	ExceptionNotFound        = "not_found"
	ExceptionInvalidRequest  = "invalid_request"
)

// CaseResult contains only non-sensitive observables needed for parity
// reports. Response bodies and request credentials are never retained.
type CaseResult struct {
	Name     string        `json:"name"`
	Status   int           `json:"status,omitempty"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	Passed   bool          `json:"passed"`
	Error    string        `json:"error,omitempty"`
}

// Report is safe to serialize and compare between embedded and sidecar runs.
type Report struct {
	TargetOrigin string       `json:"target_origin"`
	Results      []CaseResult `json:"results"`
}

// JSON serializes the redacted report. It is intentionally best-effort so
// callers can include it in an error without exposing request data.
func (r Report) JSON() string {
	data, err := json.Marshal(r)
	if err != nil {
		return `{"target_origin":"","results":[]}`
	}
	return string(data)
}

// Run executes every case in suite against target.
func Run(ctx context.Context, target Target, suite Suite) (Report, error) {
	return run(ctx, target, suite)
}
