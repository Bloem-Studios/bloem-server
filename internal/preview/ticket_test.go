package preview_test

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Silo-Server/silo-server/internal/preview"
)

var want = preview.Expectation{Issuer: "garden-operator-1", Audience: "server-1", Host: "browse.example.test"}

func sign(t *testing.T, key ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func baseClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": want.Issuer, "aud": want.Audience, "host": want.Host,
		"jti": "AAAAAAAAAAAAAAAAAAAAAA", "iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(),
		"item": "movie:42", "ref": "aff_1",
	}
}

func TestVerifyTicketAcceptsAValidTicketFromEitherKey(t *testing.T) {
	now := time.Now()
	oldPub, oldPriv := newKey(t)
	newPub, newPriv := newKey(t)
	keys := []ed25519.PublicKey{oldPub, newPub}
	for _, priv := range []ed25519.PrivateKey{oldPriv, newPriv} {
		ticket, err := preview.VerifyTicket(sign(t, priv, baseClaims(now)), keys, want, now)
		if err != nil {
			t.Fatal(err)
		}
		if ticket.ID != "AAAAAAAAAAAAAAAAAAAAAA" || ticket.Item != "movie:42" || ticket.Ref != "aff_1" {
			t.Fatalf("ticket = %+v", ticket)
		}
	}
}

func TestVerifyTicketRefusesEveryBadTicket(t *testing.T) {
	now := time.Now()
	pub, priv := newKey(t)
	_, otherPriv := newKey(t)
	keys := []ed25519.PublicKey{pub}
	cases := map[string]string{
		"just expired":       sign(t, priv, with(with(baseClaims(now), "iat", now.Add(-time.Minute).Unix()), "exp", now.Add(-time.Second).Unix())),
		"no iat":             sign(t, priv, without(baseClaims(now), "iat")),
		"backwards lifetime": sign(t, priv, with(with(baseClaims(now), "iat", now.Add(3*time.Second).Unix()), "exp", now.Add(2*time.Second).Unix())),
		"zero lifetime":      sign(t, priv, with(with(baseClaims(now), "iat", now.Add(2*time.Second).Unix()), "exp", now.Add(2*time.Second).Unix())),
		"wrong key":          sign(t, otherPriv, baseClaims(now)),
		"wrong audience":     sign(t, priv, with(baseClaims(now), "aud", "server-2")),
		"wrong issuer":       sign(t, priv, with(baseClaims(now), "iss", "garden-operator-2")),
		"wrong host":         sign(t, priv, with(baseClaims(now), "host", "lib.example.test")),
		"expired":            sign(t, priv, with(with(baseClaims(now), "iat", now.Add(-2*time.Minute).Unix()), "exp", now.Add(-time.Minute).Unix())),
		"window too long":    sign(t, priv, with(baseClaims(now), "exp", now.Add(5*time.Minute).Unix())),
		"issued later":       sign(t, priv, with(baseClaims(now), "iat", now.Add(time.Minute).Unix())),
		"no exp":             sign(t, priv, without(baseClaims(now), "exp")),
		"no jti":             sign(t, priv, without(baseClaims(now), "jti")),
		"short jti":          sign(t, priv, with(baseClaims(now), "jti", "abc")),
		"long ref":           sign(t, priv, with(baseClaims(now), "ref", strings.Repeat("r", 65))),
		"odd ref":            sign(t, priv, with(baseClaims(now), "ref", "a b")),
		"odd item":           sign(t, priv, with(baseClaims(now), "item", "../etc")),
		"hmac":               hmacTicket(t, baseClaims(now)),
		"none":               noneTicket(t, baseClaims(now)),
		"garbage":            "not-a-ticket",
	}
	for name, raw := range cases {
		if _, err := preview.VerifyTicket(raw, keys, want, now); !errors.Is(err, preview.ErrTicketInvalid) {
			t.Errorf("%s: err = %v, want ErrTicketInvalid", name, err)
		}
	}
	if _, err := preview.VerifyTicket(sign(t, priv, baseClaims(now)), nil, want, now); !errors.Is(err, preview.ErrTicketInvalid) {
		t.Errorf("no keys: err = %v", err)
	}
}

func with(c jwt.MapClaims, k string, v any) jwt.MapClaims { c[k] = v; return c }
func without(c jwt.MapClaims, k string) jwt.MapClaims     { delete(c, k); return c }

func hmacTicket(t *testing.T, c jwt.MapClaims) string {
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func noneTicket(t *testing.T, c jwt.MapClaims) string {
	raw, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
