package listcollections

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func safeText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func requiredText(s string, limit int) bool { return strings.TrimSpace(s) != "" && safeText(s, limit) }

func ValidatePage(p Page) error { return ValidatePageAt(p, time.Now()) }

// ValidatePageAt validates one transport page. Full digest verification and
// contiguous staging are performed only by the complete-snapshot consumer.
func ValidatePageAt(p Page, now time.Time) error {
	if p.Version != ProtocolVersion || p.Generation == 0 || p.Digest == (Hash{}) ||
		!requiredText(p.JobID, 256) || !requiredText(p.BindingID, 256) ||
		!requiredText(p.SourceID, 256) || !requiredText(p.SnapshotID, 256) ||
		!requiredText(p.LeaseToken, 16384) || !safeText(p.NextCursor, 16384) {
		return ErrSnapshot
	}
	if p.GeneratedAt.IsZero() || p.GeneratedAt.After(now) || !p.ExpiresAt.After(now) ||
		!p.ExpiresAt.After(p.GeneratedAt) || p.ExpiresAt.Sub(p.GeneratedAt) > SnapshotLease {
		return ErrSnapshot
	}
	if p.Total < 0 || p.Total > MaxSnapshotItems || p.Position < 0 || p.Position > p.Total ||
		len(p.Entries) > MaxPageItems || len(p.Entries) > p.Total-p.Position {
		return ErrSnapshot
	}
	end := p.Position + len(p.Entries)
	if p.Final {
		if p.NextCursor != "" || end != p.Total {
			return ErrSnapshot
		}
	} else if p.NextCursor == "" || len(p.Entries) == 0 || end >= p.Total {
		return ErrSnapshot
	}
	for _, entry := range p.Entries {
		if err := validateEntry(entry); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxSnapshotBytes {
		return ErrSnapshot
	}
	return nil
}

func validateEntry(e Entry) error {
	if e.Kind == "unsupported" {
		if !requiredText(e.Title, 4096) || !requiredText(e.SourceEntryID, 256) || e.Year < 0 || e.Year > 9999 || len(e.ExternalIDs) != 0 {
			return ErrSnapshot
		}
		switch e.UnsupportedReason {
		case "missing_identity", "unsupported_kind", "invalid_identity", "ambiguous_identity":
			return nil
		default:
			return ErrSnapshot
		}
	}
	if e.UnsupportedReason != "" || e.SourceEntryID != "" && !requiredText(e.SourceEntryID, 256) {
		return ErrSnapshot
	}
	if !safeText(e.Title, 4096) || e.Year < 0 || e.Year > 9999 || len(e.ExternalIDs) == 0 || len(e.ExternalIDs) > 5 {
		return ErrSnapshot
	}
	book := e.Kind == "ebook" || e.Kind == "audiobook"
	if !book && e.Kind != "movie" && e.Kind != "series" {
		return ErrSnapshot
	}
	for ns, id := range e.ExternalIDs {
		if !requiredText(id, 64) {
			return ErrSnapshot
		}
		switch ns {
		case "tmdb":
			if book || !positiveDecimal(id) {
				return ErrSnapshot
			}
		case "tvdb":
			if e.Kind != "series" || !positiveDecimal(id) {
				return ErrSnapshot
			}
		case "imdb":
			if book || !strings.HasPrefix(id, "tt") || len(id) < 9 || len(id) > 14 || !asciiDigits(id[2:]) {
				return ErrSnapshot
			}
		case "isbn":
			if !book || !validISBN(id) {
				return ErrSnapshot
			}
		case "asin":
			if !book || len(id) != 10 {
				return ErrSnapshot
			}
			for _, r := range id {
				if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
					return ErrSnapshot
				}
			}
		default:
			return ErrSnapshot
		}
	}
	return nil
}

func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func positiveDecimal(s string) bool {
	if !asciiDigits(s) || s[0] == '0' {
		return false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n > 0
}
func validISBN(s string) bool {
	if len(s) == 13 && asciiDigits(s) && (strings.HasPrefix(s, "978") || strings.HasPrefix(s, "979")) {
		sum := 0
		for i := 0; i < 13; i++ {
			weight := 1
			if i%2 == 1 {
				weight = 3
			}
			sum += int(s[i]-'0') * weight
		}
		return sum%10 == 0
	}
	if len(s) == 10 && asciiDigits(s[:9]) && (s[9] == 'X' || s[9] >= '0' && s[9] <= '9') {
		sum := 0
		for i := 0; i < 10; i++ {
			n := int(s[i] - '0')
			if s[i] == 'X' {
				n = 10
			}
			sum += n * (10 - i)
		}
		return sum%11 == 0
	}
	return false
}

// Digest hashes versioned canonical JSON, retaining source order and sorting
// external-ID object keys via encoding/json. Empty snapshots always use [].
func Digest(entries []Entry) (Hash, error) {
	if len(entries) > MaxSnapshotItems {
		return Hash{}, ErrSnapshot
	}
	for _, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return Hash{}, err
		}
	}
	if entries == nil {
		entries = []Entry{}
	}
	raw, err := json.Marshal(struct {
		Version int     `json:"version"`
		Entries []Entry `json:"entries"`
	}{ProtocolVersion, entries})
	if err != nil || len(raw) > MaxSnapshotBytes {
		return Hash{}, ErrSnapshot
	}
	return Hash(sha256.Sum256(raw)), nil
}

func DecodePage(raw []byte, now time.Time) (Page, error) {
	var p Page
	if len(raw) > MaxSnapshotBytes || !utf8.Valid(raw) {
		return p, ErrSnapshot
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(decoder, 0); err != nil {
		return p, ErrSnapshot
	}
	if _, err := decoder.Token(); err != io.EOF {
		return p, ErrSnapshot
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return Page{}, ErrSnapshot
	}
	if err := ValidatePageAt(p, now); err != nil {
		return Page{}, err
	}
	return p, nil
}

// uniqueJSON rejects duplicate object keys at every level before decoding into
// typed fields. A bounded nesting depth also prevents adversarial recursion.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrSnapshot
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return ErrSnapshot
			}
			seen[s] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrSnapshot
	}
	_, err = d.Token()
	return err
}
