package preview

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MaxTicketLifetime bounds exp - iat (spec §6.4).
const MaxTicketLifetime = 60 * time.Second

// ticketLeeway absorbs clock skew between Garden and the server.
const ticketLeeway = 5 * time.Second

// ErrTicketInvalid wraps every ticket refusal. Clients only ever learn that
// the ticket was refused, never why.
var ErrTicketInvalid = errors.New("preview: ticket refused")

// Expectation is what a ticket must name: Garden's operator, this server and
// the preview host.
type Expectation struct {
	Issuer   string
	Audience string
	Host     string
}

// Ticket is a verified launch ticket. Play, Grant and GrantExpiresAt are a
// free-play allowance the storefront signed; all are zero without one.
type Ticket struct {
	ID             string
	Item           string
	Ref            string
	ExpiresAt      time.Time
	Play           time.Duration
	Grant          string
	GrantExpiresAt time.Time
}

// Free-play allowance bounds (spec §6.2).
const (
	MinPlayAllowance = time.Minute
	MaxPlayAllowance = 4 * time.Hour
	MaxGrantLifetime = 7 * 24 * time.Hour
)

type ticketClaims struct {
	Host string `json:"host"`
	Item string `json:"item,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Play is the allowance in seconds; Grant and GrantExp name the storefront
	// grant it belongs to and when that grant ends (Unix seconds).
	Play     int64  `json:"play,omitempty"`
	Grant    string `json:"grant,omitempty"`
	GrantExp int64  `json:"grant_exp,omitempty"`
	jwt.RegisteredClaims
}

var (
	// 128 bits base64url-encoded is 22 characters.
	jtiPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{22,64}$`)
	refPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	itemPattern = regexp.MustCompile(`^[a-z_]+:[A-Za-z0-9._:-]{1,120}$`)
	// GrantPattern is the storefront grant identifier a play allowance names.
	GrantPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22,64}$`)
)

// VerifyTicket checks a compact EdDSA JWS against up to two public keys and
// the expectation. It does not consume the ticket; ConsumeTicket does.
func VerifyTicket(raw string, keys []ed25519.PublicKey, want Expectation, now time.Time) (Ticket, error) {
	refuse := func(reason string) (Ticket, error) {
		return Ticket{}, fmt.Errorf("%w: %s", ErrTicketInvalid, reason)
	}
	if len(keys) == 0 {
		return refuse("no ticket key is configured")
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(want.Issuer),
		jwt.WithAudience(want.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(ticketLeeway),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	var claims *ticketClaims
	var lastErr error
	for _, key := range keys {
		candidate := &ticketClaims{}
		if _, err := parser.ParseWithClaims(raw, candidate, func(*jwt.Token) (any, error) { return key, nil }); err != nil {
			lastErr = err
			continue
		}
		claims = candidate
		break
	}
	if claims == nil {
		return refuse(fmt.Sprintf("signature or claims: %v", lastErr))
	}
	if claims.Host != want.Host {
		return refuse("host")
	}
	if claims.IssuedAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > MaxTicketLifetime {
		return refuse("lifetime")
	}
	// Expiry is strict: replay records may be swept as soon as exp passes.
	if !claims.ExpiresAt.After(now) {
		return refuse("expired")
	}
	if !jtiPattern.MatchString(claims.ID) {
		return refuse("jti")
	}
	if claims.Ref != "" && !refPattern.MatchString(claims.Ref) {
		return refuse("ref")
	}
	if claims.Item != "" && !itemPattern.MatchString(claims.Item) {
		return refuse("item")
	}
	ticket := Ticket{ID: claims.ID, Item: claims.Item, Ref: claims.Ref, ExpiresAt: claims.ExpiresAt.Time}
	if claims.Play != 0 || claims.Grant != "" || claims.GrantExp != 0 {
		if reason := playAllowanceProblem(claims); reason != "" {
			return refuse(reason)
		}
		ticket.Play, ticket.Grant, ticket.GrantExpiresAt = time.Duration(claims.Play)*time.Second, claims.Grant, time.Unix(claims.GrantExp, 0)
	}
	return ticket, nil
}

// playAllowanceProblem names what is wrong with a ticket's free-play claims,
// or returns "" when all three are present and in bounds.
func playAllowanceProblem(claims *ticketClaims) string {
	play := time.Duration(claims.Play) * time.Second
	grantEnd := time.Unix(claims.GrantExp, 0)
	switch {
	case claims.Play == 0 || claims.Grant == "" || claims.GrantExp == 0:
		return "partial play allowance"
	case play < MinPlayAllowance || play > MaxPlayAllowance:
		return "play"
	case !GrantPattern.MatchString(claims.Grant):
		return "grant"
	case !grantEnd.After(claims.ExpiresAt.Time) || grantEnd.After(claims.IssuedAt.Add(MaxGrantLifetime)):
		return "grant_exp"
	}
	return ""
}
