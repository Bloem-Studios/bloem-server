// Package bookrequests owns Bloem's book request lane. Silo's TMDB requests
// remain in their own store. Fulfillment integrations are intentionally optional.
package bookrequests

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("book request forbidden")
	ErrNotFound  = errors.New("book request not found")
	ErrInvalid   = errors.New("invalid book request")
	ErrDuplicate = errors.New("book already requested")
	ErrQuota     = errors.New("book request quota exceeded")
	ErrDisabled  = errors.New("requests disabled")
	ErrConflict  = errors.New("book request state changed")
)

type Viewer struct {
	UserID         int
	ProfileID      string
	OrganizationID uuid.UUID
	Admin          bool
}

type CreateInput struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	ISBN     string `json:"isbn,omitempty"`
	ASIN     string `json:"asin,omitempty"`
	Note     string `json:"note,omitempty"`
	identity string
}

// EbookCreateInput admits ebooks separately from the existing HTTP audiobook
// input. Ebooks require an ISBN; ASIN/title identities are not
// accepted in this lane.
type EbookCreateInput struct {
	Title  string
	Author string
	ISBN   string
	Note   string
}

type Request struct {
	ID                   string     `json:"id"`
	OrganizationID       uuid.UUID  `json:"organization_id"`
	MediaType            string     `json:"media_type"`
	Title                string     `json:"title"`
	Author               string     `json:"author"`
	ISBN                 string     `json:"isbn,omitempty"`
	ASIN                 string     `json:"asin,omitempty"`
	Note                 string     `json:"note,omitempty"`
	Status               string     `json:"status"`
	Outcome              string     `json:"outcome"`
	OutcomeReason        string     `json:"outcome_reason,omitempty"`
	RequestedByUserID    int        `json:"requested_by_user_id"`
	RequestedByProfileID string     `json:"requested_by_profile_id"`
	LibraryContentID     string     `json:"library_content_id,omitempty"`
	Fulfilment           string     `json:"fulfilment"`
	Revision             int64      `json:"revision"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type Capability struct {
	Available         bool     `json:"available"`
	Enabled           bool     `json:"enabled"`
	CanRequest        bool     `json:"can_request"`
	MediaTypes        []string `json:"media_types"`
	AutoApprove       bool     `json:"auto_approve"`
	Unlimited         bool     `json:"unlimited"`
	Limit             int      `json:"limit"`
	Used              int      `json:"used"`
	Remaining         int      `json:"remaining"`
	WindowDays        int      `json:"window_days"`
	FulfilmentBackend string   `json:"fulfilment_backend"`
	ManualFulfilment  bool     `json:"manual_fulfilment"`
}

type ActionInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type ListResponse struct {
	Requests []Request `json:"requests"`
}

func canRead(v Viewer, r Request) bool {
	return v.UserID > 0 && v.OrganizationID != uuid.Nil && v.OrganizationID == r.OrganizationID &&
		(v.Admin || (v.UserID == r.RequestedByUserID && v.ProfileID != "" && v.ProfileID == r.RequestedByProfileID))
}

var asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func normalizeInput(in CreateInput) (CreateInput, error) {
	in.Title, in.Author, in.Note = strings.TrimSpace(in.Title), strings.TrimSpace(in.Author), strings.TrimSpace(in.Note)
	if in.Title == "" || !utf8.ValidString(in.Title) || len(in.Title) > 500 || len(in.Author) > 500 || len(in.Note) > 2000 {
		return in, fmt.Errorf("%w: provide a title of at most 500 bytes, author of at most 500 bytes and note of at most 2000 bytes", ErrInvalid)
	}
	var err error
	in.ISBN, err = normalizeISBN(in.ISBN)
	if err != nil {
		return in, err
	}
	in.ASIN = strings.ToUpper(strings.TrimSpace(in.ASIN))
	if in.ASIN != "" && !asinPattern.MatchString(in.ASIN) {
		return in, fmt.Errorf("%w: ASIN must contain ten letters or digits", ErrInvalid)
	}
	switch {
	case in.ISBN != "":
		in.identity = "isbn:" + in.ISBN
	case in.ASIN != "":
		in.identity = "asin:" + in.ASIN
	default:
		if in.Author == "" {
			return in, fmt.Errorf("%w: supply an author, ISBN or ASIN", ErrInvalid)
		}
		in.identity = fmt.Sprintf("title:%x", sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(in.Title), " "))+"\n"+strings.ToLower(strings.Join(strings.Fields(in.Author), " ")))))
	}
	return in, nil
}

func normalizeISBN(raw string) (string, error) {
	value := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", " ").Replace(strings.TrimSpace(raw)))
	if value == "" {
		return "", nil
	}
	if len(value) != 10 && len(value) != 13 {
		return "", fmt.Errorf("%w: ISBN must have ten or thirteen digits", ErrInvalid)
	}
	sum := 0
	for i, c := range value {
		n := int(c - '0')
		if len(value) == 10 && i == 9 && c == 'X' {
			n = 10
		}
		if n < 0 || n > 9 && (len(value) != 10 || i != 9 || n != 10) {
			return "", fmt.Errorf("%w: invalid ISBN", ErrInvalid)
		}
		if len(value) == 10 {
			sum += n * (10 - i)
		} else if i%2 == 0 {
			sum += n
		} else {
			sum += 3 * n
		}
	}
	if len(value) == 10 {
		if sum%11 != 0 {
			return "", fmt.Errorf("%w: ISBN checksum does not match", ErrInvalid)
		}
		value = "978" + value[:9]
		sum = 0
		for i, c := range value {
			n := int(c - '0')
			if i%2 == 0 {
				sum += n
			} else {
				sum += 3 * n
			}
		}
		value += fmt.Sprint((10 - sum%10) % 10)
	} else if sum%10 != 0 || !strings.HasPrefix(value, "978") && !strings.HasPrefix(value, "979") {
		return "", fmt.Errorf("%w: ISBN checksum or prefix does not match", ErrInvalid)
	}
	return value, nil
}
