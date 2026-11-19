package bookrequests

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AudiobookMarker names the per-attempt download folder shared with the
// plugin: the first eight bytes of a domain-separated SHA-256, as a
// big-endian unsigned integer zero-padded to twenty digits.
func AudiobookMarker(submission string, attempt int) string {
	sum := sha256.Sum256([]byte("bloem-audiobook-marker:v1:" + submission + ":" + strconv.Itoa(attempt)))
	return fmt.Sprintf("%020d", binary.BigEndian.Uint64(sum[:8]))
}

var errAudiobookEnvelope = errors.New("invalid audiobook acquisition envelope")

const (
	audiobookReceiptPrefix  = "abr1."
	audiobookDecisionPrefix = "abd1."
	audiobookReceiptMax     = 2048
	audiobookDecisionMax    = 6144
)

// decodeStrict accepts exactly one JSON value with no unknown fields.
func decodeStrict(prefix, raw string, max int, out any) error {
	if len(raw) > max || !strings.HasPrefix(raw, prefix) {
		return errAudiobookEnvelope
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, prefix))
	if err != nil || !utf8.Valid(body) {
		return errAudiobookEnvelope
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(out); err != nil {
		return errAudiobookEnvelope
	}
	if err = dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errAudiobookEnvelope
	}
	return nil
}

// AudiobookReceipt is the plugin's only durable memory. It never holds
// credentials, URLs or paths. Field order is part of the wire format.
type AudiobookReceipt struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	Attempt   int    `json:"attempt"`
	Client    string `json:"client"`
	Confirmed bool   `json:"confirmed"`
	Adopted   bool   `json:"adopted"`
	Sel       string `json:"sel"`
	ExpMB     int64  `json:"exp_mb"`
	ExpDurS   int64  `json:"exp_dur_s"`
}

var (
	audiobookTorrentHash = regexp.MustCompile(`^[a-f0-9]{40}$`)
	audiobookNZBID       = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	audiobookFingerprint = regexp.MustCompile(`^[a-f0-9]{16}$`)
)

// ParseAudiobookReceipt validates a receipt against the submission and attempt
// it must belong to.
func ParseAudiobookReceipt(raw, submission string, attempt int) (AudiobookReceipt, error) {
	var r AudiobookReceipt
	if err := decodeStrict(audiobookReceiptPrefix, raw, audiobookReceiptMax, &r); err != nil {
		return r, err
	}
	if r.RequestID != submission || r.Attempt != attempt || attempt < 1 || r.ExpMB < 0 || r.ExpMB > 10_000_000 || r.ExpDurS < 0 || r.ExpDurS > 10_000_000 || utf8.RuneCountInString(r.Sel) > 200 || sanitizeEnvelopeText(r.Sel) != r.Sel {
		return r, errAudiobookEnvelope
	}
	switch r.Kind {
	case "qbittorrent":
		if !audiobookTorrentHash.MatchString(r.ID) || !audiobookFingerprint.MatchString(r.Client) {
			return r, errAudiobookEnvelope
		}
	case "nzbget":
		if !audiobookNZBID.MatchString(r.ID) || !audiobookFingerprint.MatchString(r.Client) {
			return r, errAudiobookEnvelope
		}
	case "unknown":
		if r.ID != "" || r.Client != "" || r.Confirmed {
			return r, errAudiobookEnvelope
		}
	default:
		return r, errAudiobookEnvelope
	}
	return r, nil
}

// RecoveryReceipt builds the unknown receipt the host stores after a lost
// Fulfill outcome; it never contacts the plugin.
func RecoveryReceipt(submission string, attempt int) Receipt {
	raw, _ := json.Marshal(AudiobookReceipt{Kind: "unknown", RequestID: submission, Attempt: attempt})
	return Receipt{ExternalID: audiobookReceiptPrefix + base64.RawURLEncoding.EncodeToString(raw), Status: "unknown"}
}

// DecisionCandidate is one release the plugin considered.
type DecisionCandidate struct {
	Ref    string  `json:"ref"`
	Source string  `json:"source"`
	Title  string  `json:"title"`
	SizeMB int64   `json:"size_mb"`
	Format string  `json:"format,omitempty"`
	H      float64 `json:"h"`
}

type DecisionHeuristic struct {
	H      float64 `json:"h"`
	Reason string  `json:"reason"`
}

type DecisionAI struct {
	Used       bool     `json:"used"`
	Model      string   `json:"model"`
	Choice     string   `json:"choice"`
	Confidence float64  `json:"confidence"`
	Reason     string   `json:"reason"`
	Flags      []string `json:"flags"`
	Error      string   `json:"error"`
}

type DecisionSource struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// AcquisitionDecision mirrors the plugin's abd1 envelope. It is stored and
// shown only after Validate, re-serialized from this type.
type AcquisitionDecision struct {
	V             int                 `json:"v"`
	Phase         string              `json:"phase"`
	Outcome       string              `json:"outcome"`
	Detail        string              `json:"detail,omitempty"`
	Chosen        *DecisionCandidate  `json:"chosen,omitempty"`
	Candidates    []DecisionCandidate `json:"candidates,omitempty"`
	Heuristic     *DecisionHeuristic  `json:"heuristic,omitempty"`
	AI            *DecisionAI         `json:"ai,omitempty"`
	Issues        []string            `json:"issues,omitempty"`
	Queries       []string            `json:"queries,omitempty"`
	Sources       []DecisionSource    `json:"sources,omitempty"`
	AICalls       int                 `json:"ai_calls"`
	AdminApproved bool                `json:"admin_approved"`
}

var (
	audiobookCandidateRef = regexp.MustCompile(`^(abb:/abss/[A-Za-z0-9._~%-]{1,200}/|abook:[0-9]{1,10}|nzbking:[a-f0-9]{8,64})$`)
	audiobookAIChoice     = regexp.MustCompile(`^(|c([1-9]|10))$`)
	audiobookAIModel      = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{0,128}$`)
	audiobookDetail       = regexp.MustCompile(`^[a-z_]{0,32}$`)
	audiobookFormat       = regexp.MustCompile(`^[A-Za-z0-9]{0,16}$`)

	audiobookSelectOutcomes = []string{"handed_off", "adopted", "no_match", "held", "rejected", "unavailable"}
	audiobookVerifyOutcomes = []string{"verified", "review"}
	audiobookAdminOutcomes  = []string{"admin_approved", "admin_accepted"}
	audiobookSources        = []string{"audiobookbay", "abook", "nzbking"}
	audiobookSourceStatuses = []string{"ok", "empty", "rate_limited", "blocked", "error", "disabled"}
	audiobookAIErrors       = []string{"", "disabled", "budget", "timeout", "quota", "malformed", "provider_error", "config"}
	audiobookAIFlags        = []string{"abridged", "wrong_language", "wrong_book", "sample", "partial", "dramatization", "bundle", "other"}
	// Verification issues: the AI vocabulary, the heuristic checks and the
	// host-raised multiple_items.
	audiobookIssues = []string{"wrong_book", "abridged", "wrong_language", "sample", "partial", "missing_audio", "extra_content", "unsafe_content", "archive_only", "too_small", "too_short", "size_mismatch", "client_warning", "no_listing", "multiple_items", "other"}
)

// IsAudiobookCandidateRef reports whether ref has a plugin candidate shape.
func IsAudiobookCandidateRef(ref string) bool { return audiobookCandidateRef.MatchString(ref) }

// ParseAcquisitionDecision decodes and validates a plugin envelope.
func ParseAcquisitionDecision(raw string) (*AcquisitionDecision, error) {
	var d AcquisitionDecision
	if err := decodeStrict(audiobookDecisionPrefix, raw, audiobookDecisionMax, &d); err != nil {
		return nil, err
	}
	if d.Phase == "admin" {
		return nil, errAudiobookEnvelope
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Validate enforces enums, lengths and ref shapes, stripping control and
// bidirectional formatting characters from free text in place.
func (d *AcquisitionDecision) Validate() error {
	if d == nil || d.V != 1 {
		return errAudiobookEnvelope
	}
	switch d.Phase {
	case "select":
		if !slices.Contains(audiobookSelectOutcomes, d.Outcome) {
			return errAudiobookEnvelope
		}
	case "verify":
		if !slices.Contains(audiobookVerifyOutcomes, d.Outcome) {
			return errAudiobookEnvelope
		}
	case "admin":
		if !slices.Contains(audiobookAdminOutcomes, d.Outcome) {
			return errAudiobookEnvelope
		}
	default:
		return errAudiobookEnvelope
	}
	if !audiobookDetail.MatchString(d.Detail) || d.AICalls < 0 || d.AICalls > 20 || len(d.Candidates) > 5 || len(d.Issues) > 10 || len(d.Queries) > 4 || len(d.Sources) > 3 {
		return errAudiobookEnvelope
	}
	if d.Chosen != nil {
		if err := d.Chosen.validate(); err != nil {
			return err
		}
	}
	for i := range d.Candidates {
		if err := d.Candidates[i].validate(); err != nil {
			return err
		}
	}
	if d.Heuristic != nil {
		if !unitInterval(d.Heuristic.H) {
			return errAudiobookEnvelope
		}
		var err error
		if d.Heuristic.Reason, err = boundedText(d.Heuristic.Reason, 300); err != nil {
			return err
		}
	}
	if d.AI != nil {
		a := d.AI
		if !audiobookAIModel.MatchString(a.Model) || !audiobookAIChoice.MatchString(a.Choice) || !unitInterval(a.Confidence) || !slices.Contains(audiobookAIErrors, a.Error) || len(a.Flags) > 5 {
			return errAudiobookEnvelope
		}
		for _, flag := range a.Flags {
			if !slices.Contains(audiobookAIFlags, flag) {
				return errAudiobookEnvelope
			}
		}
		var err error
		if a.Reason, err = boundedText(a.Reason, 300); err != nil {
			return err
		}
		if a.Flags == nil {
			a.Flags = []string{}
		}
	}
	for _, issue := range d.Issues {
		if !slices.Contains(audiobookIssues, issue) {
			return errAudiobookEnvelope
		}
	}
	for i, query := range d.Queries {
		var err error
		if d.Queries[i], err = boundedText(query, 120); err != nil {
			return err
		}
	}
	for _, source := range d.Sources {
		if !slices.Contains(audiobookSources, source.Name) || !slices.Contains(audiobookSourceStatuses, source.Status) {
			return errAudiobookEnvelope
		}
	}
	if len(d.Issues) == 0 {
		d.Issues = nil
	}
	return nil
}

func (c *DecisionCandidate) validate() error {
	if !audiobookCandidateRef.MatchString(c.Ref) || !slices.Contains(audiobookSources, c.Source) || c.SizeMB < 0 || c.SizeMB > 10_000_000 || !unitInterval(c.H) || !audiobookFormat.MatchString(c.Format) {
		return errAudiobookEnvelope
	}
	var err error
	c.Title, err = boundedText(c.Title, 200)
	return err
}

// Encode re-serializes a validated decision for storage.
func (d *AcquisitionDecision) Encode() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(d)
}

func unitInterval(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }

func boundedText(s string, runes int) (string, error) {
	s = sanitizeEnvelopeText(s)
	if utf8.RuneCountInString(s) > runes {
		return "", errAudiobookEnvelope
	}
	return s, nil
}

// sanitizeEnvelopeText drops control characters and bidirectional formatting
// overrides. Text is otherwise carried verbatim and rendered as plain text.
func sanitizeEnvelopeText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return -1
		case r == '؜' || r == '‎' || r == '‏' || r >= '‪' && r <= '‮' || r >= '⁦' && r <= '⁩':
			return -1
		}
		return r
	}, s)
}
