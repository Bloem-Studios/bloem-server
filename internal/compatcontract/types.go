// Package compatcontract executes protocol fixtures against a compatibility
// listener. It deliberately contains no Bloem-domain dependencies so the
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
//
// Bindings resolve the {{name}} placeholders fixtures use for values only the
// system under test knows — seeded usernames, profile identifiers, and the
// credentials that logins mint. Cases with a Capture write into this same
// map, so a token issued by one case (or one Run) is the token later cases
// spend. The map may therefore hold live credentials: it belongs to the
// caller, is never copied into a Report, and must never be logged.
type Target struct {
	BaseURL     string
	Client      *http.Client
	Credentials CredentialProvider
	Bindings    map[string]string
}

// Suite is a named protocol surface characterized by Cases.
type Suite struct {
	Name  string
	Cases []Case
}

// Pick returns a copy of the suite containing exactly the named cases, in the
// order given. Consumers use it to run a suite in phases when state must
// change between cases — a revision rotated, a credential reset — while the
// full suite remains one document. Unknown names make the picked suite carry
// a poisoned case so a stale name fails loudly instead of silently shrinking
// the contract.
func (s Suite) Pick(names ...string) Suite {
	byName := make(map[string]Case, len(s.Cases))
	for _, c := range s.Cases {
		byName[c.Name] = c
	}
	picked := Suite{Name: s.Name}
	for _, name := range names {
		c, ok := byName[name]
		if !ok {
			c = Case{Name: "missing fixture case: " + name}
		}
		picked.Cases = append(picked.Cases, c)
	}
	return picked
}

// Case records one observable protocol interaction. Fixture IDs and URLs must
// be invented and use reserved domains only. Path, BodyJSON, header values,
// present/absent strings, expected field values, the timing control path, and
// the websocket path all expand {{name}} placeholders from Target.Bindings;
// an unbound placeholder fails the case.
//
// BodyJSON is the request body as readable JSON (preferred); Body carries
// opaque bytes for non-JSON payloads and is sent verbatim.
//
// WantJSONCounts asserts how many elements the arrays selected by dotted
// paths contain ("$" is the response root, "$.Items" a field, "$.0.Name"
// indexes an array). Counts freeze non-disclosure — an empty profile
// directory, an adult-free catalog page — without the report ever retaining
// a body.
//
// WantJSONFields asserts selected values semantically: the response JSON is
// parsed and the selected value must deep-equal the expected JSON, so a
// privilege flag is pinned as the field it is rather than as a byte substring
// a formatting change could defeat.
//
// Capture stores selected response values into Target.Bindings under the
// given names (binding name -> selector), before any assertion runs, so a
// login case that fails an assertion still hands its issued credential to the
// cases that must prove the credential is bounded.
//
// WantWebSocketFiltered watches the socket for a bounded window and accepts
// any amount of legitimate traffic — keepalives, ordinary events — while
// failing the case if any frame carries an excluded string. It freezes what
// events must not say, not that the server stays silent.
type Case struct {
	Name                  string                     `json:"name"`
	Method                string                     `json:"method,omitempty"`
	Path                  string                     `json:"path"`
	Body                  []byte                     `json:"body,omitempty"`
	BodyJSON              json.RawMessage            `json:"body_json,omitempty"`
	Headers               map[string]string          `json:"headers,omitempty"`
	Capture               map[string]string          `json:"capture,omitempty"`
	WantStatus            int                        `json:"want_status,omitempty"`
	WantHeaders           map[string]string          `json:"want_headers,omitempty"`
	WantJSON              json.RawMessage            `json:"want_json,omitempty"`
	WantJSONCounts        map[string]int             `json:"want_json_counts,omitempty"`
	WantJSONFields        map[string]json.RawMessage `json:"want_json_fields,omitempty"`
	WantSHA256            string                     `json:"want_sha256,omitempty"`
	WantWebSocketJSON     []json.RawMessage          `json:"want_websocket_json,omitempty"`
	WantWebSocketFiltered bool                       `json:"want_websocket_filtered,omitempty"`
	Exception             string                     `json:"exception,omitempty"`
	PresentStrings        []string                   `json:"present_strings,omitempty"`
	AbsentStrings         []string                   `json:"absent_strings,omitempty"`
	Timing                *TimingDistribution        `json:"timing,omitempty"`
}

// TimingDistribution compares a protected missing ID with an unrelated
// missing ID. Samples are interleaved protected/control pairs; every sample —
// protected and control alike — must return the same status code and satisfy
// the case's negative expectation, so a control that answers differently is
// itself a finding. The medians must agree within MaxRatio, with a 2ms
// absolute floor for scheduler noise: a stable measurable delay on only the
// protected path fails, no matter how far under any fixed allowance it sits.
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

// FailedCases returns the names of every case that did not pass, in order.
// Mutation tests use it to prove a single seeded violation fails exactly the
// case that owns the behavior and nothing else.
func (r Report) FailedCases() []string {
	var failed []string
	for _, result := range r.Results {
		if !result.Passed {
			failed = append(failed, result.Name)
		}
	}
	return failed
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
